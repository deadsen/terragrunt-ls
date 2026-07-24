package definition_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"terragrunt-ls/internal/testutils"
	"terragrunt-ls/internal/tg"
	"terragrunt-ls/internal/tg/definition"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

func TestGetDefinitionTargetWithContext(t *testing.T) {
	t.Parallel()

	tc := []struct {
		name            string
		document        string
		expectedTarget  string
		expectedContext string
		position        protocol.Position
	}{
		{
			name:            "empty store",
			document:        "",
			position:        protocol.Position{Line: 0, Character: 0},
			expectedTarget:  "",
			expectedContext: "null",
		},
		{
			name: "include definition",
			document: `include "root" {
	path = find_in_parent_folders("root")
}`,
			position:        protocol.Position{Line: 1, Character: 8},
			expectedTarget:  "root",
			expectedContext: "include",
		},
		{
			name: "dependency definition",
			document: `dependency "vpc" {
	config_path = "../vpc"
}`,
			position:        protocol.Position{Line: 1, Character: 18},
			expectedTarget:  "vpc",
			expectedContext: "dependency",
		},
	}

	for _, tt := range tc {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			l := testutils.NewTestLogger(t)

			s := tg.NewState()

			s.OpenDocument(context.Background(), l, "file:///test.hcl", tt.document, 1)

			target, context := definition.GetDefinitionTargetWithContext(l, s.Configs["/test.hcl"], tt.position)

			assert.Equal(t, tt.expectedTarget, target)
			assert.Equal(t, tt.expectedContext, context)
		})
	}
}

func TestResolveRichTerragruntDefinitions(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	unitDir := filepath.Join(tmpDir, "unit")
	appDir := filepath.Join(tmpDir, "app")
	require.NoError(t, os.MkdirAll(unitDir, 0o755))
	require.NoError(t, os.MkdirAll(appDir, 0o755))

	appConfig := filepath.Join(appDir, "terragrunt.hcl")
	rootConfig := filepath.Join(tmpDir, "root.hcl")
	dataFile := filepath.Join(unitDir, "data.json")
	require.NoError(t, os.WriteFile(appConfig, nil, 0o600))
	require.NoError(t, os.WriteFile(rootConfig, nil, 0o600))
	require.NoError(t, os.WriteFile(dataFile, []byte("{}"), 0o600))

	source := `locals {
  dep_dir = "../app"
  data    = file("data.json")
}

include "root" {
  path = find_in_parent_folders("root.hcl")
  expose = true
}

dependency "app" {
  config_path = local.dep_dir
}

inputs = {
  id     = dependency.app.outputs.id
  common = include.root.inputs
}`
	unitFile := filepath.Join(unitDir, "terragrunt.hcl")
	require.NoError(t, os.WriteFile(unitFile, []byte(source), 0o600))
	docURI := uri.File(unitFile)
	l := testutils.NewTestLogger(t)
	state := tg.NewState()
	require.Empty(t, state.OpenDocument(t.Context(), l, docURI, source, 1))
	st := state.Configs[unitFile]

	tests := []struct {
		name     string
		file     string
		position protocol.Position
		line     uint32
	}{
		{name: "local reference", file: unitFile, position: protocol.Position{Line: 11, Character: 24}, line: 1},
		{name: "dependency config path", file: appConfig, position: protocol.Position{Line: 11, Character: 4}},
		{name: "dependency reference", file: appConfig, position: protocol.Position{Line: 15, Character: 23}},
		{name: "include path", file: rootConfig, position: protocol.Position{Line: 6, Character: 10}},
		{name: "include reference", file: rootConfig, position: protocol.Position{Line: 16, Character: 20}},
		{name: "file call", file: dataFile, position: protocol.Position{Line: 2, Character: 20}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			locations := definition.Resolve(st, docURI, tt.position)
			require.Len(t, locations, 1)
			assert.Equal(t, uri.File(tt.file), locations[0].URI)
			assert.Equal(t, tt.line, locations[0].Range.Start.Line)
		})
	}
}

func TestResolveFileThroughFindInParentFolders(t *testing.T) {
	t.Parallel()

	rootDir := t.TempDir()
	sourceDir := filepath.Join(rootDir, "environments", "development")
	require.NoError(t, os.MkdirAll(sourceDir, 0o755))

	environmentFile := filepath.Join(rootDir, "environment.yaml")
	require.NoError(t, os.WriteFile(environmentFile, []byte("name: development\n"), 0o600))

	source := `locals {
  environment = yamldecode(file("${find_in_parent_folders("environment.yaml")}"))
}`
	sourceFile := filepath.Join(sourceDir, "terragrunt.hcl")
	require.NoError(t, os.WriteFile(sourceFile, []byte(source), 0o600))

	docURI := uri.File(sourceFile)
	state := tg.NewState()
	require.Empty(t, state.OpenDocument(t.Context(), testutils.NewTestLogger(t), docURI, source, 1))
	st := state.Configs[sourceFile]

	for _, target := range []string{"find_in_parent_folders", "environment.yaml"} {
		locations := definition.Resolve(st, docURI, positionWithin(source, target))

		require.Len(t, locations, 1)
		assert.Equal(t, uri.File(environmentFile), locations[0].URI)
	}

	require.NoError(t, os.Remove(environmentFile))
	assert.Empty(t, definition.Resolve(st, docURI, positionWithin(source, "environment.yaml")))
}

func positionWithin(source, target string) protocol.Position {
	offset := strings.Index(source, target)
	if offset < 0 {
		return protocol.Position{}
	}
	offset += len(target) / 2
	prefix := source[:offset]
	lines := strings.Split(prefix, "\n")

	return protocol.Position{Line: uint32(len(lines) - 1), Character: uint32(len(lines[len(lines)-1]))}
}
