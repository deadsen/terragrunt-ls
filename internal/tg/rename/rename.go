// Package rename provides the logic for renaming identifiers in Terragrunt configurations.
package rename

import (
	"regexp"

	"terragrunt-ls/internal/ast"
	"terragrunt-ls/internal/logger"
	"terragrunt-ls/internal/tg/store"
	"terragrunt-ls/internal/tg/symbol"

	"go.lsp.dev/protocol"
)

const (
	// RenameContextLocal is the context for renaming a local variable
	// (`locals { name = ... }` and `local.name` references).
	RenameContextLocal      = "local"
	RenameContextDependency = "dependency"
	RenameContextInclude    = "include"

	// RenameContextNull means the cursor is not on a renameable identifier.
	RenameContextNull = "null"
)

// RenameTarget describes the symbol resolved at the cursor position.
type RenameTarget struct {
	// Name is the current identifier value.
	Name string
	// Context is one of the RenameContext* constants.
	Context string
	// IdentRange is the LSP range covering only the identifier token, suitable
	// for use as the prepare-rename range.
	IdentRange protocol.Range
}

// Occurrence is a single text span (in a specific file) of the target symbol.
// IsDefinition is true for the symbol's declaration site, false for references.
type Occurrence struct {
	File         string
	Range        protocol.Range
	IsDefinition bool
}

// hclIdentifierRE matches a valid HCL identifier (also accepts hyphens, which
// are valid in block labels though not in unquoted variable references).
var hclIdentifierRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// IsValidIdentifier reports whether s is a valid HCL identifier.
func IsValidIdentifier(s string) bool {
	return hclIdentifierRE.MatchString(s)
}

// GetRenameTarget identifies the renameable symbol at the given position.
// Returns a target with Context == RenameContextNull when nothing renameable
// is at the position.
func GetRenameTarget(l logger.Logger, st store.Store, position protocol.Position) RenameTarget {
	null := RenameTarget{Context: RenameContextNull}

	if st.AST == nil {
		l.Debug("No AST found for rename")
		return null
	}

	target, ok := symbol.At(st.AST, st.Document, position)
	if !ok {
		l.Debug("No node at position", "line", position.Line, "character", position.Character)
		return null
	}

	return RenameTarget{
		Name:       target.Name,
		Context:    string(target.Kind),
		IdentRange: ast.FromHCLRange(st.Document, target.Range),
	}
}

// FindAllOccurrences returns every occurrence of target within the given file's
// AST: the declaration site (when present) plus all references. The returned
// slice is sorted by (line, column) for determinism.
func FindAllOccurrences(target RenameTarget, file string, st store.Store) []Occurrence {
	if target.Context == RenameContextNull || st.AST == nil {
		return nil
	}

	resolved := symbol.Target{Kind: symbol.Kind(target.Context), Name: target.Name}
	symbolOccurrences := symbol.Occurrences(st.AST, st.Document, resolved, true)
	occurrences := make([]Occurrence, 0, len(symbolOccurrences))
	for _, occurrence := range symbolOccurrences {
		occurrences = append(occurrences, Occurrence{
			File:         file,
			Range:        occurrence.Range,
			IsDefinition: occurrence.Declaration,
		})
	}

	return occurrences
}
