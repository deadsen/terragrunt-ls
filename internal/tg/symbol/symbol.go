// Package symbol resolves Terragrunt declarations and references within a file.
package symbol

import (
	"sort"

	"terragrunt-ls/internal/ast"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"go.lsp.dev/protocol"
)

const quotedLabelDelimiterBytes = 2

// Kind identifies a Terragrunt symbol namespace.
type Kind string

const (
	Local      Kind = "local"
	Dependency Kind = "dependency"
	Include    Kind = "include"
)

// Target describes the symbol under a cursor.
type Target struct {
	Kind        Kind
	Name        string
	Range       hcl.Range
	EditRange   hcl.Range
	Declaration bool
}

// Occurrence is a declaration or reference to a symbol.
type Occurrence struct {
	Range       protocol.Range
	Declaration bool
}

// At resolves a declaration or reference at position.
func At(indexed *ast.IndexedAST, source string, position protocol.Position) (Target, bool) {
	if indexed == nil {
		return Target{}, false
	}

	node := indexed.FindNodeAt(ast.ToHCLPos(source, position))
	if node == nil {
		return Target{}, false
	}

	for current := node; current != nil; current = current.Parent {
		switch typed := current.Node.(type) {
		case *hclsyntax.Attribute:
			if ast.IsLocalAttribute(current) && containsPosition(ast.FromHCLRange(source, typed.NameRange), position) {
				return Target{Kind: Local, Name: typed.Name, Range: typed.NameRange, EditRange: typed.NameRange, Declaration: true}, true
			}
		case *hclsyntax.Block:
			kind, ok := blockKind(current)
			if !ok || len(typed.Labels) == 0 || len(typed.LabelRanges) == 0 {
				continue
			}

			if containsPosition(ast.FromHCLRange(source, typed.LabelRanges[0]), position) {
				return Target{
					Kind:        kind,
					Name:        typed.Labels[0],
					Range:       typed.LabelRanges[0],
					EditRange:   bareLabelRange(typed.LabelRanges[0]),
					Declaration: true,
				}, true
			}
		case *hclsyntax.ScopeTraversalExpr:
			if target, ok := traversalTarget(typed, source, position); ok {
				return target, true
			}
		}
	}

	return Target{}, false
}

// Occurrences returns the sorted, unique occurrences of target in indexed.
func Occurrences(indexed *ast.IndexedAST, source string, target Target, includeDeclaration bool) []Occurrence {
	if indexed == nil || indexed.HCLFile == nil {
		return nil
	}

	body, ok := indexed.HCLFile.Body.(*hclsyntax.Body)
	if !ok || body == nil {
		return nil
	}

	occurrences := make([]Occurrence, 0)
	seen := make(map[protocol.Range]struct{})
	appendOccurrence := func(sourceRange hcl.Range, declaration bool) {
		if declaration && !includeDeclaration {
			return
		}

		converted := ast.FromHCLRange(source, sourceRange)
		if _, exists := seen[converted]; exists {
			return
		}

		seen[converted] = struct{}{}
		occurrences = append(occurrences, Occurrence{Range: converted, Declaration: declaration})
	}

	if declaration := declarationRange(indexed, target); declaration != nil {
		appendOccurrence(*declaration, true)
	}

	ast.WalkReferences(body, string(target.Kind), target.Name, func(_ *hclsyntax.ScopeTraversalExpr, sourceRange hcl.Range) {
		appendOccurrence(sourceRange, false)
	})

	sort.Slice(occurrences, func(i, j int) bool {
		if occurrences[i].Range.Start.Line != occurrences[j].Range.Start.Line {
			return occurrences[i].Range.Start.Line < occurrences[j].Range.Start.Line
		}

		return occurrences[i].Range.Start.Character < occurrences[j].Range.Start.Character
	})

	return occurrences
}

func traversalTarget(expr *hclsyntax.ScopeTraversalExpr, source string, position protocol.Position) (Target, bool) {
	if len(expr.Traversal) < ast.MinReferenceTraversalLen {
		return Target{}, false
	}

	root, rootOK := expr.Traversal[0].(hcl.TraverseRoot)

	attribute, attributeOK := expr.Traversal[1].(hcl.TraverseAttr)
	if !rootOK || !attributeOK {
		return Target{}, false
	}

	kind, ok := traversalKind(root.Name)
	if !ok {
		return Target{}, false
	}

	identifierRange := ast.TraverseAttrIdentRange(attribute)

	firstTwoSteps := hcl.Range{
		Filename: root.SrcRange.Filename,
		Start:    root.SrcRange.Start,
		End:      attribute.SrcRange.End,
	}
	if !containsPosition(ast.FromHCLRange(source, firstTwoSteps), position) {
		return Target{}, false
	}

	return Target{Kind: kind, Name: attribute.Name, Range: identifierRange, EditRange: identifierRange}, true
}

func bareLabelRange(labelRange hcl.Range) hcl.Range {
	result := labelRange
	if result.End.Byte-result.Start.Byte >= quotedLabelDelimiterBytes {
		result.Start.Byte++
		result.Start.Column++
		result.End.Byte--
		result.End.Column--
	}

	return result
}

func traversalKind(root string) (Kind, bool) {
	switch root {
	case string(Local):
		return Local, true
	case string(Dependency):
		return Dependency, true
	case string(Include):
		return Include, true
	default:
		return "", false
	}
}

func blockKind(node *ast.IndexedNode) (Kind, bool) {
	switch {
	case ast.IsDependencyBlock(node):
		return Dependency, true
	case ast.IsIncludeBlock(node):
		return Include, true
	default:
		return "", false
	}
}

func declarationRange(indexed *ast.IndexedAST, target Target) *hcl.Range {
	var scope ast.Scope

	switch target.Kind {
	case Local:
		scope = indexed.Locals
	case Dependency:
		scope = indexed.Dependencies
	case Include:
		scope = indexed.Includes
	default:
		return nil
	}

	node, ok := scope[target.Name]
	if !ok {
		return nil
	}

	switch typed := node.Node.(type) {
	case *hclsyntax.Attribute:
		result := typed.NameRange
		return &result
	case *hclsyntax.Block:
		if len(typed.LabelRanges) == 0 {
			return nil
		}

		result := typed.LabelRanges[0]

		return &result
	default:
		return nil
	}
}

func containsPosition(sourceRange protocol.Range, position protocol.Position) bool {
	if position.Line < sourceRange.Start.Line || position.Line > sourceRange.End.Line {
		return false
	}

	if position.Line == sourceRange.Start.Line && position.Character < sourceRange.Start.Character {
		return false
	}

	if position.Line == sourceRange.End.Line && position.Character >= sourceRange.End.Character {
		return false
	}

	return true
}
