package symbol_test

import (
	"testing"

	"terragrunt-ls/internal/ast"
	"terragrunt-ls/internal/tg/symbol"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
)

func TestAtRecognizesTerragruntDeclarationsAndReferences(t *testing.T) {
	t.Parallel()

	source := `locals {
  region = "eu-west-1"
}
dependency "app" {
  config_path = "../app"
}
include "root" {
  path = find_in_parent_folders()
}
inputs = {
  region = local.region
  id     = dependency.app.outputs.id
  common = include.root.inputs
}`
	indexed, err := ast.ParseHCLFile("terragrunt.hcl", []byte(source))
	require.NoError(t, err)

	tests := []struct {
		name        string
		position    protocol.Position
		kind        symbol.Kind
		symbolName  string
		declaration bool
	}{
		{"local declaration", protocol.Position{Line: 1, Character: 3}, symbol.Local, "region", true},
		{"dependency declaration", protocol.Position{Line: 3, Character: 13}, symbol.Dependency, "app", true},
		{"include declaration", protocol.Position{Line: 6, Character: 10}, symbol.Include, "root", true},
		{"local reference", protocol.Position{Line: 10, Character: 19}, symbol.Local, "region", false},
		{"dependency reference", protocol.Position{Line: 11, Character: 20}, symbol.Dependency, "app", false},
		{"include reference", protocol.Position{Line: 12, Character: 20}, symbol.Include, "root", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target, ok := symbol.At(indexed, source, tt.position)
			require.True(t, ok)
			assert.Equal(t, tt.kind, target.Kind)
			assert.Equal(t, tt.symbolName, target.Name)
			assert.Equal(t, tt.declaration, target.Declaration)
		})
	}
}

func TestOccurrencesReturnsSortedUniqueDeclarationsAndReferences(t *testing.T) {
	t.Parallel()

	source := `dependency "app" {
  config_path = "../app"
}
inputs = {
  first  = dependency.app.outputs.id
  second = dependency.app.outputs.name
}`
	indexed, err := ast.ParseHCLFile("terragrunt.hcl", []byte(source))
	require.NoError(t, err)

	target, ok := symbol.At(indexed, source, protocol.Position{Line: 4, Character: 23})
	require.True(t, ok)

	withDeclaration := symbol.Occurrences(indexed, source, target, true)
	require.Len(t, withDeclaration, 3)
	assert.True(t, withDeclaration[0].Declaration)
	assert.Equal(t, uint32(0), withDeclaration[0].Range.Start.Line)
	assert.Equal(t, uint32(4), withDeclaration[1].Range.Start.Line)
	assert.Equal(t, uint32(5), withDeclaration[2].Range.Start.Line)

	withoutDeclaration := symbol.Occurrences(indexed, source, target, false)
	require.Len(t, withoutDeclaration, 2)
	assert.False(t, withoutDeclaration[0].Declaration)
	assert.False(t, withoutDeclaration[1].Declaration)
}
