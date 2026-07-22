// Package references provides the logic for finding all references of an
// identifier within a Terragrunt unit.
package references

import (
	"terragrunt-ls/internal/logger"
	"terragrunt-ls/internal/tg/store"
	"terragrunt-ls/internal/tg/symbol"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// GetReferences returns LSP locations for every reference (and optionally the
// declaration) of the renameable symbol at position. Returns nil if the cursor
// is not on a renameable identifier.
func GetReferences(l logger.Logger, st store.Store, position protocol.Position, file string, includeDeclaration bool) []protocol.Location {
	target, ok := symbol.At(st.AST, st.Document, position)
	if !ok {
		l.Debug("No symbol found for references", "line", position.Line, "character", position.Character)
		return nil
	}

	occurrences := symbol.Occurrences(st.AST, st.Document, target, includeDeclaration)
	if len(occurrences) == 0 {
		return nil
	}

	locations := make([]protocol.Location, 0, len(occurrences))

	for _, occ := range occurrences {
		locations = append(locations, protocol.Location{
			URI:   uri.File(file),
			Range: occ.Range,
		})
	}

	return locations
}
