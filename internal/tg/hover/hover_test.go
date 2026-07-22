package hover_test

import (
	"terragrunt-ls/internal/ast"
	"terragrunt-ls/internal/testutils"
	"terragrunt-ls/internal/tg/hover"
	"terragrunt-ls/internal/tg/store"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
)

func TestGetHoverTargetWithContext(t *testing.T) {
	t.Parallel()

	tc := []struct {
		name            string
		expectedTarget  string
		expectedContext string
		store           store.Store
		position        protocol.Position
	}{
		{
			name:            "empty document",
			store:           store.Store{},
			position:        protocol.Position{Line: 0, Character: 0},
			expectedContext: "null",
		},
		{
			name:            "local variable",
			store:           store.Store{Document: "inputs = local.var"},
			position:        protocol.Position{Line: 0, Character: 9},
			expectedTarget:  "var",
			expectedContext: "local",
		},
	}

	for _, tt := range tc {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			l := testutils.NewTestLogger(t)

			target, context := hover.GetHoverTargetWithContext(l, tt.store, tt.position)

			assert.Equal(t, tt.expectedTarget, target)
			assert.Equal(t, tt.expectedContext, context)
		})
	}
}

func TestGetLocalPathNested(t *testing.T) {
	t.Parallel()

	document := `locals {
  service = { database = { port = 5432 } }
}
inputs = { port = local.service.database.port }`
	indexed, err := ast.ParseHCLFile("terragrunt.hcl", []byte(document))
	require.NoError(t, err)

	path, selectedRange, ok := hover.GetLocalPath(
		store.Store{AST: indexed, Document: document},
		protocol.Position{Line: 3, Character: 43},
	)

	require.True(t, ok)
	assert.Equal(t, []string{"service", "database", "port"}, path)
	assert.Equal(t, protocol.Position{Line: 3, Character: 41}, selectedRange.Start)
	assert.Equal(t, protocol.Position{Line: 3, Character: 45}, selectedRange.End)
}
