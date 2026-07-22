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
	"terragrunt-ls/internal/lsp"
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
	message string
	reached chan struct{}
	release chan struct{}
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

	tc := []struct {
		expected lsp.HoverResponse
		name     string
		document string
		position protocol.Position
	}{
		{
			name: "simple locals",
			document: `locals {
	foo = "bar"
	bar = local.foo
}`,
			position: protocol.Position{
				Line:      2,
				Character: 15,
			},
			expected: lsp.HoverResponse{
				Response: lsp.Response{
					RPC: "2.0",
					ID:  testutils.PointerOfInt(1),
				},
				Result: lsp.HoverResult{
					Contents: protocol.MarkupContent{
						Kind:  protocol.Markdown,
						Value: "```hcl\nfoo = \"bar\"\n```",
					},
				},
			},
		},
		{
			name: "interpolated locals",
			document: `locals {
	foo = "bar"
	baz = "${local.foo}-baz"
	qux = local.baz
}`,
			position: protocol.Position{
				Line:      3,
				Character: 15,
			},
			expected: lsp.HoverResponse{
				Response: lsp.Response{
					RPC: "2.0",
					ID:  testutils.PointerOfInt(1),
				},
				Result: lsp.HoverResult{
					Contents: protocol.MarkupContent{
						Kind:  protocol.Markdown,
						Value: "```hcl\nbaz = \"bar-baz\"\n```",
					},
				},
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

			hover := state.Hover(l, 1, "file:///foo/terragrunt.hcl", tt.position)
			assert.Equal(t, tt.expected, hover)
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

	result := state.Hover(l, 1, docURI, protocol.Position{Line: 3, Character: 43})

	assert.Contains(t, result.Result.Contents.Value, "port = 5432")
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

	result := state.Hover(l, 1, docURI, protocol.Position{Line: 3, Character: 31})

	assert.Empty(t, result.Result.Contents.Value)
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
		position protocol.Position
		expected []protocol.Location
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

			response := state.Definition(l, 1, unitURI, tt.position)
			assert.Equal(t, lsp.Response{RPC: "2.0", ID: testutils.PointerOfInt(1)}, response.Response)
			assert.Equal(t, tt.expected, response.Result)
		})
	}
}

func TestState_TextDocumentCompletion(t *testing.T) {
	t.Parallel()

	tc := []struct {
		name              string
		initial           string
		document          string
		expected          lsp.CompletionResponse
		position          protocol.Position
		expectDiagnostics bool
	}{
		{
			name:     "complete dep",
			document: "dep",
			position: protocol.Position{
				Line:      0,
				Character: 3,
			},
			expectDiagnostics: true,
			expected: lsp.CompletionResponse{
				Response: lsp.Response{
					RPC: "2.0",
					ID:  testutils.PointerOfInt(1),
				},
				Result: []protocol.CompletionItem{
					{
						Label: "dependency",
						Documentation: protocol.MarkupContent{
							Kind:  protocol.Markdown,
							Value: "# dependency\nThe dependency block is used to configure unit dependencies.\nEach dependency block exposes outputs of the dependency unit as variables you can reference in dependent unit configuration.",
						},
						Kind:             protocol.CompletionItemKindClass,
						InsertTextFormat: protocol.InsertTextFormatSnippet,
						TextEdit: &protocol.TextEdit{
							Range: protocol.Range{
								Start: protocol.Position{Line: 0, Character: 0},
								End:   protocol.Position{Line: 0, Character: 3},
							},
							NewText: `dependency "${1}" {
	config_path = "${2}"
}`,
						},
					},
					{
						Label: "dependencies",
						Documentation: protocol.MarkupContent{
							Kind:  protocol.Markdown,
							Value: "# dependencies\nThe dependencies block is used to enumerate all the Terragrunt units that need to be applied before this unit.",
						},
						Kind:             protocol.CompletionItemKindClass,
						InsertTextFormat: protocol.InsertTextFormatSnippet,
						TextEdit: &protocol.TextEdit{
							Range: protocol.Range{
								Start: protocol.Position{Line: 0, Character: 0},
								End:   protocol.Position{Line: 0, Character: 3},
							},
							NewText: `dependencies {
	paths = ["${1}"]
}`,
						},
					},
				},
			},
		},
	}

	for _, tt := range tc {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			state := tg.NewState()
			l := testutils.NewTestLogger(t)

			diags := state.OpenDocument(t.Context(), l, "file:///terragrunt.hcl", tt.document, 1)
			if tt.expectDiagnostics {
				require.NotEmpty(t, diags)
			} else {
				require.Empty(t, diags)
			}

			completion := state.TextDocumentCompletion(l, 1, "file:///terragrunt.hcl", tt.position)
			assert.Equal(t, tt.expected, completion)
		})
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

	completion := state.TextDocumentCompletion(l, 1, stackURI, protocol.Position{Line: 0, Character: 3})

	require.Len(t, completion.Result, 1)
	assert.Equal(t, "unit", completion.Result[0].Label)
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

	completion := state.TextDocumentCompletion(l, 1, valuesURI, protocol.Position{Line: 0, Character: 3})

	assert.Empty(t, completion.Result)
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

	hover := state.Hover(l, 1, stackURI, protocol.Position{Line: 0, Character: 0})
	assert.Empty(t, hover.Result.Contents.Value)
}

func TestState_Hover_ValuesFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	valuesPath := filepath.Join(tmpDir, "terragrunt.values.hcl")
	valuesURI := uri.File(valuesPath)

	state := tg.NewState()
	l := testutils.NewTestLogger(t)

	_ = state.OpenDocument(t.Context(), l, valuesURI, `some_var = "hello"`, 1)

	hover := state.Hover(l, 1, valuesURI, protocol.Position{Line: 0, Character: 0})
	assert.Empty(t, hover.Result.Contents.Value)
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
	def := state.Definition(l, 1, stackURI, pos)
	assert.Empty(t, def.Result)
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
			response := state.TextDocumentFormatting(l, 1, "file:///terragrunt.hcl")

			// Verify the formatting result
			require.Len(t, response.Result, 1)
			assert.Equal(t, tt.expected, response.Result[0].NewText)

			assert.Equal(t, uint32(0), response.Result[0].Range.Start.Line)
			assert.Equal(t, uint32(0), response.Result[0].Range.Start.Character)

			lines := strings.Split(tt.document, "\n")
			assert.Equal(t, uint32(len(lines)-1), response.Result[0].Range.End.Line)
			assert.Equal(t, uint32(len(lines[len(lines)-1])), response.Result[0].Range.End.Character)
		})
	}
}
