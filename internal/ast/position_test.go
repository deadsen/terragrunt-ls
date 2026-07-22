package ast_test

import (
	"testing"

	"terragrunt-ls/internal/ast"

	"github.com/hashicorp/hcl/v2"
	"github.com/stretchr/testify/assert"
	"go.lsp.dev/protocol"
)

func TestPositionConversionsUseUTF16CodeUnits(t *testing.T) {
	t.Parallel()

	source := "😀a\n"
	hclPosition := hcl.Pos{Line: 1, Column: 6, Byte: 5}

	lspPosition := ast.FromHCLPos(source, hclPosition)

	assert.Equal(t, protocol.Position{Line: 0, Character: 3}, lspPosition)
	assert.Equal(t, hclPosition, ast.ToHCLPos(source, lspPosition))
}

func TestRangeConversionsPreserveUTF16PositionsAndByteOffsets(t *testing.T) {
	t.Parallel()

	source := "x = 😀value\n"
	hclRange := hcl.Range{
		Start: hcl.Pos{Line: 1, Column: 5, Byte: 4},
		End:   hcl.Pos{Line: 1, Column: 14, Byte: 13},
	}

	lspRange := ast.FromHCLRange(source, hclRange)

	assert.Equal(t, protocol.Range{
		Start: protocol.Position{Line: 0, Character: 4},
		End:   protocol.Position{Line: 0, Character: 11},
	}, lspRange)
	assert.Equal(t, hclRange, ast.ToHCLRange(source, lspRange))
}
