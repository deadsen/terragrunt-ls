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

func TestState_PrepareRename(t *testing.T) {
	t.Parallel()

	tc := []struct {
		name      string
		document  string
		wantPlace string
		position  protocol.Position
		wantStart protocol.Position
		wantEnd   protocol.Position
		wantNil   bool
	}{
		{
			name: "local definition",
			document: `locals {
  foo = "bar"
}`,
			position:  protocol.Position{Line: 1, Character: 3},
			wantPlace: "foo",
			wantStart: protocol.Position{Line: 1, Character: 2},
			wantEnd:   protocol.Position{Line: 1, Character: 5},
		},
		{
			name: "local reference",
			document: `locals { foo = "bar" }
inputs = { v = local.foo }`,
			position:  protocol.Position{Line: 1, Character: 23},
			wantPlace: "foo",
			wantStart: protocol.Position{Line: 1, Character: 21},
			wantEnd:   protocol.Position{Line: 1, Character: 24},
		},
		{
			name: "non-renameable position returns nil",
			document: `locals {
  foo = "bar"
}`,
			position: protocol.Position{Line: 0, Character: 0},
			wantNil:  true,
		},
	}

	for _, tt := range tc {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tmpDir := t.TempDir()
			tgPath := filepath.Join(tmpDir, "terragrunt.hcl")
			docURI := uri.File(tgPath)

			l := testutils.NewTestLogger(t)
			s := tg.NewState()
			s.OpenDocument(t.Context(), l, docURI, tt.document, 1)

			renameRange, placeholder, found := s.PrepareRename(l, docURI, tt.position)

			if tt.wantNil {
				assert.False(t, found)
				return
			}

			require.True(t, found)
			assert.Equal(t, tt.wantPlace, placeholder)
			assert.Equal(t, tt.wantStart, renameRange.Start)
			assert.Equal(t, tt.wantEnd, renameRange.End)
		})
	}
}

func TestState_TextDocumentRename(t *testing.T) {
	t.Parallel()

	t.Run("rejects invalid identifier", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		tgPath := filepath.Join(tmpDir, "terragrunt.hcl")
		docURI := uri.File(tgPath)

		l := testutils.NewTestLogger(t)
		s := tg.NewState()
		s.OpenDocument(t.Context(), l, docURI, `locals { foo = "bar" }`, 1)

		resp := s.TextDocumentRename(l, docURI, protocol.Position{Line: 0, Character: 9}, "1invalid")
		assert.Nil(t, resp)
	})

	t.Run("renames local declaration and references in same file", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		tgPath := filepath.Join(tmpDir, "terragrunt.hcl")
		docURI := uri.File(tgPath)

		content := `locals {
  shared = "value"
}

inputs = {
  v = local.shared
}
`
		_, err := testutils.CreateFile(tmpDir, "terragrunt.hcl", content)
		require.NoError(t, err)

		l := testutils.NewTestLogger(t)
		s := tg.NewState()
		s.OpenDocument(t.Context(), l, docURI, content, 1)

		resp := s.TextDocumentRename(l, docURI, protocol.Position{Line: 5, Character: 14}, "renamed")
		require.NotNil(t, resp)
		require.NotNil(t, resp.Changes)

		edits := resp.Changes[docURI]
		require.Len(t, edits, 2, "definition + reference in the same file")

		for _, edit := range edits {
			assert.Equal(t, "renamed", edit.NewText)
		}
	})

	t.Run("returns nil for non-renameable position", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		tgPath := filepath.Join(tmpDir, "terragrunt.hcl")
		docURI := uri.File(tgPath)

		l := testutils.NewTestLogger(t)
		s := tg.NewState()
		s.OpenDocument(t.Context(), l, docURI, `locals { foo = "bar" }`, 1)

		resp := s.TextDocumentRename(l, docURI, protocol.Position{Line: 0, Character: 0}, "valid")
		assert.Nil(t, resp)
	})

	t.Run("works on auxiliary HCL files (FileTypeUnknown)", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		commonPath := filepath.Join(tmpDir, "common.hcl")
		docURI := uri.File(commonPath)

		l := testutils.NewTestLogger(t)
		s := tg.NewState()
		s.OpenDocument(t.Context(), l, docURI, `locals { foo = "bar" }`, 1)

		resp := s.TextDocumentRename(l, docURI, protocol.Position{Line: 0, Character: 9}, "renamed")
		require.NotNil(t, resp)

		edits := resp.Changes[docURI]
		require.Len(t, edits, 1)
		assert.Equal(t, "renamed", edits[0].NewText)
	})
}

func TestState_DependencyAndIncludeRename(t *testing.T) {
	t.Parallel()

	content := `dependency "app" {
  config_path = "../app"
}

include "root" {
  path = "root.hcl"
}

inputs = {
  id = dependency.app.outputs.id
  x  = include.root.inputs.x
}`
	tests := []struct {
		name          string
		position      protocol.Position
		newName       string
		prepareStart  protocol.Position
		prepareEnd    protocol.Position
		declarationAt protocol.Position
		referenceAt   protocol.Position
	}{
		{
			name:          "dependency",
			position:      protocol.Position{Line: 0, Character: 13},
			newName:       "service",
			prepareStart:  protocol.Position{Line: 0, Character: 12},
			prepareEnd:    protocol.Position{Line: 0, Character: 15},
			declarationAt: protocol.Position{Line: 0, Character: 11},
			referenceAt:   protocol.Position{Line: 9, Character: 18},
		},
		{
			name:          "include",
			position:      protocol.Position{Line: 4, Character: 10},
			newName:       "parent",
			prepareStart:  protocol.Position{Line: 4, Character: 9},
			prepareEnd:    protocol.Position{Line: 4, Character: 13},
			declarationAt: protocol.Position{Line: 4, Character: 8},
			referenceAt:   protocol.Position{Line: 10, Character: 15},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			tgPath := filepath.Join(tmpDir, "terragrunt.hcl")
			docURI := uri.File(tgPath)
			l := testutils.NewTestLogger(t)
			state := tg.NewState()
			state.OpenDocument(t.Context(), l, docURI, content, 1)

			prepareRange, _, found := state.PrepareRename(l, docURI, tt.position)
			require.True(t, found)
			assert.Equal(t, tt.prepareStart, prepareRange.Start)
			assert.Equal(t, tt.prepareEnd, prepareRange.End)

			rename := state.TextDocumentRename(l, docURI, tt.position, tt.newName)
			require.NotNil(t, rename)
			edits := rename.Changes[docURI]
			require.Len(t, edits, 2)
			assert.Equal(t, tt.declarationAt, edits[0].Range.Start)
			assert.Equal(t, "\""+tt.newName+"\"", edits[0].NewText)
			assert.Equal(t, tt.referenceAt, edits[1].Range.Start)
			assert.Equal(t, tt.newName, edits[1].NewText)
		})
	}
}
