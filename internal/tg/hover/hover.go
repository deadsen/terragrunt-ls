// Package hover provides the logic for determining the target of a hover.
package hover

import (
	"terragrunt-ls/internal/ast"
	"terragrunt-ls/internal/logger"
	"terragrunt-ls/internal/tg/store"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"go.lsp.dev/protocol"
)

const (
	// HoverContextLocal is the context for a local hover.
	HoverContextLocal = "local"

	// HoverContextNull means the cursor is not on a hoverable value.
	HoverContextNull = "null"
)

func GetHoverTargetWithContext(l logger.Logger, st store.Store, position protocol.Position) (string, string) {
	path, _, ok := GetLocalPath(st, position)
	if !ok {
		l.Debug("No local path found", "line", position.Line, "character", position.Character)
		return "", HoverContextNull
	}

	name := path[len(path)-1]
	l.Debug("Found local variable", "line", position.Line, "character", position.Character, "local", name)

	return name, HoverContextLocal
}

// GetLocalPath returns the local traversal path selected at position and the
// range of its final selected attribute.
func GetLocalPath(st store.Store, position protocol.Position) ([]string, protocol.Range, bool) {
	if st.AST == nil {
		indexed, _ := ast.ParseHCLFile("", []byte(st.Document))

		st.AST = indexed
		if st.AST == nil {
			return nil, protocol.Range{}, false
		}
	}

	node := st.AST.FindNodeAt(ast.ToHCLPos(st.Document, position))
	if node == nil {
		return nil, protocol.Range{}, false
	}

	var expression *hclsyntax.ScopeTraversalExpr

	for current := node; current != nil; current = current.Parent {
		if candidate, ok := current.Node.(*hclsyntax.ScopeTraversalExpr); ok {
			expression = candidate
			break
		}
	}

	if expression == nil || len(expression.Traversal) < ast.MinReferenceTraversalLen {
		return nil, protocol.Range{}, false
	}

	root, ok := expression.Traversal[0].(hcl.TraverseRoot)
	if !ok || root.Name != HoverContextLocal {
		return nil, protocol.Range{}, false
	}

	if !containsPosition(ast.FromHCLRange(st.Document, expression.Range()), position) {
		return nil, protocol.Range{}, false
	}

	path := make([]string, 0, len(expression.Traversal)-1)

	var selected protocol.Range

	for _, traversal := range expression.Traversal[1:] {
		attribute, ok := traversal.(hcl.TraverseAttr)
		if !ok {
			break
		}

		path = append(path, attribute.Name)

		selected = ast.FromHCLRange(st.Document, ast.TraverseAttrIdentRange(attribute))
		if containsPosition(ast.FromHCLRange(st.Document, attribute.SrcRange), position) {
			return path, selected, true
		}
	}

	if len(path) == 0 {
		return nil, protocol.Range{}, false
	}

	if containsPosition(ast.FromHCLRange(st.Document, root.SrcRange), position) {
		first := expression.Traversal[1].(hcl.TraverseAttr)
		return path[:1], ast.FromHCLRange(st.Document, ast.TraverseAttrIdentRange(first)), true
	}

	return path, selected, true
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
