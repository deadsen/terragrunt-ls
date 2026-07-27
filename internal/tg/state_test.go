package tg_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gruntwork-io/terragrunt/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"terragrunt-ls/internal/logger"
	"terragrunt-ls/internal/testutils"
	"terragrunt-ls/internal/tg"
	"terragrunt-ls/internal/tg/store"
)

func TestNewState(t *testing.T) {
	t.Parallel()

	state := tg.NewState()

	assert.NotNil(t, state.Configs)
}

func TestStateVersionedLifecycle(t *testing.T) {
	t.Parallel()

	l := testutils.NewTestLogger(t)
	s := tg.NewState()
	uri := protocol.DocumentURI("file:///tmp/env.hcl")

	require.Empty(t, s.OpenDocument(t.Context(), l, uri, "locals { a = 1 }", 3))
	st, ok := s.Document(uri)
	require.True(t, ok)
	assert.Equal(t, store.FileTypeUnit, st.FileType)
	assert.Equal(t, int32(3), st.Version)

	_, applied := s.UpdateDocumentWithStatus(t.Context(), l, uri, "locals { a = 2 }", 2)
	assert.False(t, applied)
	st, _ = s.Document(uri)
	assert.Equal(t, "locals { a = 1 }", st.Document)

	invalid := "locals {"
	require.NotEmpty(t, s.UpdateDocument(t.Context(), l, uri, invalid, 4))
	require.NotEmpty(t, s.SaveDocument(t.Context(), l, uri), "save must reanalyse the stored document")
	s.CloseDocument(uri)
	_, ok = s.Document(uri)
	assert.False(t, ok)
}

func TestStateReportsMissingParentFileBeforeDependencyPath(t *testing.T) {

	t.Parallel()

	filename := filepath.Join(t.TempDir(), "environment", "terragrunt.hcl")
	require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o755))

	diagnostics := tg.NewState().OpenDocument(t.Context(), testutils.NewTestLogger(t), uri.File(filename), `
locals {
  account = yamldecode(file("${find_in_parent_folders("accounts.yaml")}"))
}

dependency "enhanced-monitoring-role" {
  config_path = "../${local.account.name}"
}
`, 1)

	messages := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		messages = append(messages, diagnostic.Message)
	}

	joinedMessages := strings.Join(messages, "\n")
	assert.Contains(t, joinedMessages, "accounts.yaml")
	assert.NotContains(t, joinedMessages, `Could not evaluate dependency "enhanced-monitoring-role" config_path to a concrete string path.`)
}

func TestStateSaveDoesNotRestoreClosedDocument(t *testing.T) {
	t.Parallel()

	l := testutils.NewTestLogger(t)
	s := tg.NewState()
	uri := protocol.DocumentURI("file:///tmp/save-close.hcl")
	require.Empty(t, s.OpenDocument(t.Context(), l, uri, "locals { value = 1 }", 1))

	blockingLog := newBlockingDebugLogger(l, "Config")
	done := make(chan bool, 1)
	go func() {
		_, applied := s.SaveDocumentWithStatus(t.Context(), blockingLog, uri)
		done <- applied
	}()

	blockingLog.wait(t)
	s.CloseDocument(uri)
	blockingLog.unblock()
	assert.False(t, <-done)

	_, ok := s.Document(uri)
	assert.False(t, ok)
}

func TestStateCloseThenReopenRejectsPreviousLifecycle(t *testing.T) {
	t.Parallel()

	l := testutils.NewTestLogger(t)
	s := tg.NewState()
	uri := protocol.DocumentURI("file:///tmp/close-reopen.hcl")
	blockingLog := newBlockingDebugLogger(l, "Config")
	done := make(chan bool, 1)
	go func() {
		_, applied := s.OpenDocumentWithStatus(t.Context(), blockingLog, uri, "locals { value = \"old\" }", 1)
		done <- applied
	}()

	blockingLog.wait(t)
	s.CloseDocument(uri)
	require.Empty(t, s.OpenDocument(t.Context(), l, uri, "locals { value = \"new\" }", 1))
	blockingLog.unblock()
	assert.False(t, <-done)

	st, ok := s.Document(uri)
	require.True(t, ok)
	assert.Equal(t, "locals { value = \"new\" }", st.Document)
}

func TestStateConcurrentDocumentAccess(t *testing.T) {
	t.Parallel()

	l := testutils.NewTestLogger(t)
	s := tg.NewState()
	const documents = 8

	var wg sync.WaitGroup
	for i := range documents {
		wg.Add(1)
		go func() {
			defer wg.Done()
			uri := protocol.DocumentURI(fmt.Sprintf("file:///tmp/concurrent-%d.hcl", i))
			s.OpenDocument(t.Context(), l, uri, "locals { value = 1 }", 1)
			s.UpdateDocument(t.Context(), l, uri, "locals { value = 2 }", 2)
			_, _ = s.Document(uri)
		}()
	}
	wg.Wait()

	for i := range documents {
		uri := protocol.DocumentURI(fmt.Sprintf("file:///tmp/concurrent-%d.hcl", i))
		st, ok := s.Document(uri)
		require.True(t, ok)
		assert.Equal(t, int32(2), st.Version)
	}
}

type blockingDebugLogger struct {
	logger.Logger
	reached chan struct{}
	release chan struct{}
	message string
	once    sync.Once
}

func newBlockingDebugLogger(l logger.Logger, message string) *blockingDebugLogger {
	return &blockingDebugLogger{
		Logger:  l,
		message: message,
		reached: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (l *blockingDebugLogger) Debug(message string, args ...any) {
	if message == l.message {
		l.once.Do(func() { close(l.reached) })
		<-l.release
	}
	l.Logger.Debug(message, args...)
}

func (l *blockingDebugLogger) wait(t *testing.T) {
	t.Helper()

	select {
	case <-l.reached:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for analysis to reach commit boundary")
	}
}

func (l *blockingDebugLogger) unblock() {
	close(l.release)
}

func TestState_OpenDocument(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	_, err := testutils.CreateFile(tmpDir, "root.hcl", "")
	require.NoError(t, err)

	rootPath := filepath.Join(tmpDir, "root.hcl")

	// rootURI := uri.File(rootPath)

	unitDir := filepath.Join(tmpDir, "foo")

	err = os.MkdirAll(unitDir, 0755)
	require.NoError(t, err)

	// Create the URI for the unit file
	unitPath := filepath.Join(unitDir, "terragrunt.hcl")

	unitURI := uri.File(unitPath)

	tc := []struct {
		expectedLocals         map[string]any
		expectedFieldsMetadata map[string]map[string]any
		expectedIncludes       config.IncludeConfigsMap
		name                   string
		document               string
		expectedDeps           config.Dependencies
	}{
		{
			name:             "empty document",
			document:         "",
			expectedIncludes: config.IncludeConfigsMap{},
		},
		{
			name: "simple locals",
			document: `locals {
	foo = "bar"
}`,
			expectedLocals: map[string]any{
				"foo": "bar",
			},
			expectedIncludes: config.IncludeConfigsMap{},
			expectedFieldsMetadata: map[string]map[string]any{
				"locals-foo": {
					"found_in_file": unitPath,
				},
			},
		},
		{
			name: "multiple locals",
			document: `locals {
	foo = "bar"
	baz = "qux"
}`,
			expectedLocals: map[string]any{
				"baz": "qux",
				"foo": "bar",
			},
			expectedIncludes: config.IncludeConfigsMap{},
			expectedFieldsMetadata: map[string]map[string]any{
				"locals-baz": {
					"found_in_file": unitPath,
				},
				"locals-foo": {
					"found_in_file": unitPath,
				},
			},
		},
		{
			name: "root include",
			document: `include "root" {
	path = find_in_parent_folders("root.hcl")
}`,
			expectedIncludes: config.IncludeConfigsMap{
				"root": {
					Name: "root",
					Path: rootPath,
				},
			},
			expectedDeps: config.Dependencies{},
		},
	}

	for _, tt := range tc {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			state := tg.NewState()

			l := testutils.NewTestLogger(t)

			diags := state.OpenDocument(t.Context(), l, unitURI, tt.document, 1)
			require.Empty(t, diags)

			assert.Len(t, state.Configs, 1)

			cfg := state.Configs[unitPath].Cfg
			require.NotNil(t, cfg)
			assert.Equal(t, tt.expectedLocals, cfg.Locals)
			assert.Equal(t, tt.expectedIncludes, cfg.ProcessedIncludes)
			assert.Equal(t, tt.expectedFieldsMetadata, cfg.FieldsMetadata)
			assert.Empty(t, cfg.GenerateConfigs)

			if tt.expectedDeps != nil {
				assert.Equal(t, tt.expectedDeps, cfg.TerragruntDependencies)
			}
		})
	}
}

func TestStateDiagnosticSnapshotIsMergedAndSorted(t *testing.T) {
	t.Parallel()

	source := `inputs = {
  z = include.missing.inputs.z
  a = local.missing
}`
	docURI := protocol.DocumentURI("file:///tmp/terragrunt.hcl")
	l := testutils.NewTestLogger(t)
	state := tg.NewState()

	diags := state.OpenDocument(t.Context(), l, docURI, source, 1)

	require.NotEmpty(t, diags)
	stored, ok := state.Document(docURI)
	require.True(t, ok)
	assert.Equal(t, diags, stored.Diagnostics)
	for i := 1; i < len(diags); i++ {
		previous := diags[i-1]
		current := diags[i]
		assert.True(t,
			previous.Range.Start.Line < current.Range.Start.Line ||
				previous.Range.Start.Line == current.Range.Start.Line && previous.Range.Start.Character <= current.Range.Start.Character,
		)
	}
}

func TestState_UpdateDocument(t *testing.T) {
	t.Parallel()

	tc := []struct {
		expected        map[string]any
		expectedUpdated map[string]any
		name            string
		document        string
		updated         string
	}{
		{
			name:     "empty document",
			document: "",
		},
		{
			name: "simple locals",
			document: `locals {
	foo = "bar"
}`,
			expected: map[string]any{
				"foo": "bar",
			},
			updated: `locals {
	foo = "baz"
}`,
			expectedUpdated: map[string]any{
				"foo": "baz",
			},
		},
		{
			name: "multiple locals",
			document: `locals {
	foo = "bar"
	baz = "qux"
}`,
			expected: map[string]any{
				"foo": "bar",
				"baz": "qux",
			},
			updated: `locals {
	foo = "baz"
	baz = "qux"
}`,
			expectedUpdated: map[string]any{
				"foo": "baz",
				"baz": "qux",
			},
		},
	}

	for _, tt := range tc {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			state := tg.NewState()

			l := testutils.NewTestLogger(t)

			diags := state.OpenDocument(t.Context(), l, "file:///foo/terragrunt.hcl", tt.document, 1)
			assert.Empty(t, diags)

			require.Len(t, state.Configs, 1)

			if len(tt.expected) != 0 {
				assert.Equal(t, tt.expected, state.Configs["/foo/terragrunt.hcl"].Cfg.Locals)
			}

			diags = state.UpdateDocument(t.Context(), l, "file:///foo/terragrunt.hcl", tt.updated, 2)
			assert.Empty(t, diags)

			assert.Len(t, state.Configs, 1)

			if len(tt.expectedUpdated) != 0 {
				assert.Equal(t, tt.expectedUpdated, state.Configs["/foo/terragrunt.hcl"].Cfg.Locals)
			}
		})
	}
}

func TestState_Hover(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		document string
		expected string
		position protocol.Position
	}{
		{
			name: "simple locals",
			document: `locals {
	foo = "bar"
	bar = local.foo
}`,
			position: protocol.Position{Line: 2, Character: 15},
			expected: "```hcl\nfoo = \"bar\"\n```",
		},
		{
			name: "interpolated locals",
			document: `locals {
	foo = "bar"
	baz = "${local.foo}-baz"
	qux = local.baz
}`,
			position: protocol.Position{Line: 3, Character: 15},
			expected: "```hcl\nbaz = \"bar-baz\"\n```",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			state := tg.NewState()
			l := testutils.NewTestLogger(t)
			diags := state.OpenDocument(t.Context(), l, "file:///foo/terragrunt.hcl", tt.document, 1)
			assert.Empty(t, diags)
			hover := state.Hover(l, "file:///foo/terragrunt.hcl", tt.position)
			require.NotNil(t, hover)
			assert.Equal(t, tt.expected, hover.Contents.Value)
		})
	}
}

func TestState_Hover_NestedLocal(t *testing.T) {
	t.Parallel()

	source := `locals {
  service = { database = { port = 5432 } }
}
inputs = { port = local.service.database.port }`
	l := testutils.NewTestLogger(t)
	state := tg.NewState()
	docURI := protocol.DocumentURI("file:///tmp/terragrunt.hcl")
	require.Empty(t, state.OpenDocument(t.Context(), l, docURI, source, 1))

	result := state.Hover(l, docURI, protocol.Position{Line: 3, Character: 43})

	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "port = 5432")
}

func TestState_Hover_MarkedValueIsHidden(t *testing.T) {
	t.Parallel()

	source := `locals {
  secret = "not-for-hover"
}
inputs = { value = local.secret }`
	l := testutils.NewTestLogger(t)
	state := tg.NewState()
	docURI := protocol.DocumentURI("file:///tmp/terragrunt.hcl")
	require.Empty(t, state.OpenDocument(t.Context(), l, docURI, source, 1))

	stored := state.Configs[docURI.Filename()]
	stored.CfgAsCty = stored.CfgAsCty.Mark("sensitive")
	state.Configs[docURI.Filename()] = stored

	result := state.Hover(l, docURI, protocol.Position{Line: 3, Character: 31})

	assert.Nil(t, result)
}

func TestState_Definition(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	_, err := testutils.CreateFile(tmpDir, "root.hcl", "")
	require.NoError(t, err)
	rootURI := uri.File(filepath.Join(tmpDir, "root.hcl"))

	vpcDir := filepath.Join(tmpDir, "vpc")
	err = os.MkdirAll(vpcDir, 0o755)
	require.NoError(t, err)
	_, err = testutils.CreateFile(vpcDir, "terragrunt.hcl", "")
	require.NoError(t, err)
	vpcURI := uri.File(filepath.Join(vpcDir, "terragrunt.hcl"))

	unitDir := filepath.Join(tmpDir, "foo")
	err = os.MkdirAll(unitDir, 0o755)
	require.NoError(t, err)
	unitURI := uri.File(filepath.Join(unitDir, "terragrunt.hcl"))

	tests := []struct {
		name     string
		document string
		expected []protocol.Location
		position protocol.Position
	}{
		{
			name: "nothing to jump to",
			document: `locals {
	foo = "bar"
	bar = local.foo
}`,
			position: protocol.Position{Line: 0, Character: 0},
			expected: []protocol.Location{},
		},
		{
			name: "go to root include",
			document: `include "root" {
	path = find_in_parent_folders("root.hcl")
}`,
			position: protocol.Position{Line: 1, Character: 8},
			expected: []protocol.Location{{URI: rootURI, Range: protocol.Range{}}},
		},
		{
			name: "go to dependency",
			document: `dependency "vpc" {
    config_path = "../vpc"
}`,
			position: protocol.Position{Line: 1, Character: 18},
			expected: []protocol.Location{{URI: vpcURI, Range: protocol.Range{}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			state := tg.NewState()
			l := testutils.NewTestLogger(t)
			diags := state.OpenDocument(t.Context(), l, unitURI, tt.document, 1)
			assert.Empty(t, diags)
			require.Len(t, state.Configs, 1)

			response := state.Definition(unitURI, tt.position)
			assert.Equal(t, tt.expected, response)
		})
	}
}

func TestState_TextDocumentCompletion(t *testing.T) {
	t.Parallel()

	state := tg.NewState()
	l := testutils.NewTestLogger(t)
	document := "dep"
	diags := state.OpenDocument(t.Context(), l, "file:///terragrunt.hcl", document, 1)
	require.NotEmpty(t, diags)

	items := state.TextDocumentCompletion(l, "file:///terragrunt.hcl", protocol.Position{Line: 0, Character: 3})
	require.Len(t, items, 2)
	assert.Equal(t, "dependency", items[0].Label)
	assert.Equal(t, "dependencies", items[1].Label)
	for _, item := range items {
		require.NotNil(t, item.TextEdit)
		assert.Equal(t, protocol.Range{
			Start: protocol.Position{Line: 0, Character: 0},
			End:   protocol.Position{Line: 0, Character: 3},
		}, item.TextEdit.Range)
	}
}

func TestState_TextDocumentCompletion_StackFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	stackPath := filepath.Join(tmpDir, "terragrunt.stack.hcl")
	stackURI := uri.File(stackPath)

	state := tg.NewState()
	l := testutils.NewTestLogger(t)

	// "uni" is incomplete HCL, so the stack parser will produce diagnostics — that's expected.
	diags := state.OpenDocument(t.Context(), l, stackURI, "uni", 1)
	require.NotEmpty(t, diags)

	completion := state.TextDocumentCompletion(l, stackURI, protocol.Position{Line: 0, Character: 3})

	require.Len(t, completion, 1)
	assert.Equal(t, "unit", completion[0].Label)
}

func TestState_TextDocumentCompletion_ValuesFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	valuesPath := filepath.Join(tmpDir, "terragrunt.values.hcl")
	valuesURI := uri.File(valuesPath)

	state := tg.NewState()
	l := testutils.NewTestLogger(t)

	diags := state.OpenDocument(t.Context(), l, valuesURI, "loc", 1)
	assert.Empty(t, diags)

	completion := state.TextDocumentCompletion(l, valuesURI, protocol.Position{Line: 0, Character: 3})

	assert.Empty(t, completion)
}

func TestState_OpenDocument_StackFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	stackPath := filepath.Join(tmpDir, "terragrunt.stack.hcl")
	stackURI := uri.File(stackPath)

	state := tg.NewState()
	l := testutils.NewTestLogger(t)

	diags := state.OpenDocument(t.Context(), l, stackURI, `unit "vpc" {
	source = "./units/vpc"
	path   = "vpc"
}`, 1)
	assert.Empty(t, diags)

	require.Len(t, state.Configs, 1)

	st := state.Configs[stackPath]
	assert.NotNil(t, st.StackCfg)
	assert.Nil(t, st.Cfg)
	assert.Len(t, st.StackCfg.Units, 1)
	assert.Equal(t, "vpc", st.StackCfg.Units[0].Name)
}

func TestState_OpenDocument_ValuesFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	valuesPath := filepath.Join(tmpDir, "terragrunt.values.hcl")
	valuesURI := uri.File(valuesPath)

	state := tg.NewState()
	l := testutils.NewTestLogger(t)

	diags := state.OpenDocument(t.Context(), l, valuesURI, `some_var = "hello"`, 1)
	assert.Empty(t, diags)

	require.Len(t, state.Configs, 1)

	st := state.Configs[valuesPath]
	assert.Nil(t, st.Cfg)
	assert.Nil(t, st.StackCfg)
}

func TestState_Hover_StackFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	stackPath := filepath.Join(tmpDir, "terragrunt.stack.hcl")
	stackURI := uri.File(stackPath)

	state := tg.NewState()
	l := testutils.NewTestLogger(t)

	_ = state.OpenDocument(t.Context(), l, stackURI, `unit "vpc" {
	source = "./units/vpc"
	path   = "vpc"
}`, 1)

	hover := state.Hover(l, stackURI, protocol.Position{Line: 0, Character: 0})
	assert.Nil(t, hover)
}

func TestState_Hover_ValuesFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	valuesPath := filepath.Join(tmpDir, "terragrunt.values.hcl")
	valuesURI := uri.File(valuesPath)

	state := tg.NewState()
	l := testutils.NewTestLogger(t)

	_ = state.OpenDocument(t.Context(), l, valuesURI, `some_var = "hello"`, 1)

	hover := state.Hover(l, valuesURI, protocol.Position{Line: 0, Character: 0})
	assert.Nil(t, hover)
}

func TestState_Definition_StackFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	stackPath := filepath.Join(tmpDir, "terragrunt.stack.hcl")
	stackURI := uri.File(stackPath)

	state := tg.NewState()
	l := testutils.NewTestLogger(t)

	_ = state.OpenDocument(t.Context(), l, stackURI, `unit "vpc" {
	source = "./units/vpc"
	path   = "vpc"
}`, 1)

	pos := protocol.Position{Line: 0, Character: 0}
	def := state.Definition(stackURI, pos)
	assert.Empty(t, def)
}

func TestState_TextDocumentFormatting(t *testing.T) {
	t.Parallel()

	tc := []struct {
		name     string
		document string
		expected string
	}{
		{
			name:     "empty document",
			document: "",
			expected: "",
		},
		{
			name: "unformatted locals",
			document: `locals{
foo="bar"
bar=   "baz"
}`,
			expected: `locals {
  foo = "bar"
  bar = "baz"
}`,
		},
		{
			name: "already formatted locals",
			document: `locals {
  foo = "bar"
  bar = "baz"
}`,
			expected: `locals {
  foo = "bar"
  bar = "baz"
}`,
		},
	}

	for _, tt := range tc {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			state := tg.NewState()
			l := testutils.NewTestLogger(t)

			// First open the document to populate the state
			diags := state.OpenDocument(t.Context(), l, "file:///terragrunt.hcl", tt.document, 1)
			require.Empty(t, diags)

			// Request formatting
			response := state.TextDocumentFormatting(l, "file:///terragrunt.hcl")

			// Verify the formatting result
			require.Len(t, response, 1)
			assert.Equal(t, tt.expected, response[0].NewText)

			assert.Equal(t, uint32(0), response[0].Range.Start.Line)
			assert.Equal(t, uint32(0), response[0].Range.Start.Character)

			lines := strings.Split(tt.document, "\n")
			assert.Equal(t, uint32(len(lines)-1), response[0].Range.End.Line)
			assert.Equal(t, uint32(len(lines[len(lines)-1])), response[0].Range.End.Character)
		})
	}
}
