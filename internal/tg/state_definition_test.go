package tg_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"terragrunt-ls/internal/testutils"
	"terragrunt-ls/internal/tg"
)

func TestState_Definition_LocalReference_SameFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	tgPath := filepath.Join(tmpDir, "terragrunt.hcl")
	docURI := uri.File(tgPath)

	content := `locals {
  foo = "bar"
}

inputs = {
  v = local.foo
}
`
	_, err := testutils.CreateFile(tmpDir, "terragrunt.hcl", content)
	require.NoError(t, err)

	l := testutils.NewTestLogger(t)
	s := tg.NewState()
	s.OpenDocument(t.Context(), l, docURI, content, 1)

	// Cursor on `foo` in `local.foo`.
	resp := s.Definition(l, 1, docURI, protocol.Position{Line: 5, Character: 14})

	require.Len(t, resp.Result, 1)
	assert.Equal(t, docURI, resp.Result[0].URI)
	assert.Equal(t, uint32(1), resp.Result[0].Range.Start.Line)
	assert.Equal(t, uint32(2), resp.Result[0].Range.Start.Character)
	assert.Equal(t, uint32(5), resp.Result[0].Range.End.Character)
}

func TestState_Definition_LocalReference_NotFound(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	tgPath := filepath.Join(tmpDir, "terragrunt.hcl")
	docURI := uri.File(tgPath)

	content := `inputs = {
  v = local.nonexistent
}
`
	_, err := testutils.CreateFile(tmpDir, "terragrunt.hcl", content)
	require.NoError(t, err)

	l := testutils.NewTestLogger(t)
	s := tg.NewState()
	s.OpenDocument(t.Context(), l, docURI, content, 1)

	// Cursor on `nonexistent` — no `locals` block defines it.
	resp := s.Definition(l, 1, docURI, protocol.Position{Line: 1, Character: 18})

	assert.Empty(t, resp.Result)
}
