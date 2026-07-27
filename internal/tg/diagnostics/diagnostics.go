// Package diagnostics adds Terragrunt-aware semantic diagnostics.
package diagnostics

import (
	"fmt"
	"sort"
	"strings"

	"terragrunt-ls/internal/ast"
	terragruntpath "terragrunt-ls/internal/tg/path"
	"terragrunt-ls/internal/tg/store"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"go.lsp.dev/protocol"
)

const sourceName = "Terragrunt"

const unknownValueDiagnostic = `Unsuitable value type: Unsuitable value: value must be known`

const parentFileNotFoundDiagnostic = "ParentFileNotFoundError"

// Validate returns semantic diagnostics for declarations and traversals.
func Validate(filename, source string, st store.Store) []protocol.Diagnostic {
	if st.AST == nil || st.AST.HCLFile == nil {
		return nil
	}

	body, ok := st.AST.HCLFile.Body.(*hclsyntax.Body)
	if !ok || body == nil {
		return nil
	}

	result := make([]protocol.Diagnostic, 0)
	_ = hclsyntax.VisitAll(body, func(node hclsyntax.Node) hcl.Diagnostics {
		expression, ok := node.(*hclsyntax.ScopeTraversalExpr)
		if !ok || len(expression.Traversal) < ast.MinReferenceTraversalLen {
			return nil
		}

		root, rootOK := expression.Traversal[0].(hcl.TraverseRoot)

		attribute, attributeOK := expression.Traversal[1].(hcl.TraverseAttr)
		if !rootOK || !attributeOK {
			return nil
		}

		var (
			scope   ast.Scope
			message string
		)

		switch root.Name {
		case "local":
			scope = st.AST.Locals
			message = fmt.Sprintf(`No local named %q exists in this file.`, attribute.Name)
		case "dependency":
			scope = st.AST.Dependencies
			message = fmt.Sprintf(`No dependency block named %q exists in this file.`, attribute.Name)
		case "include":
			scope = st.AST.Includes
			message = fmt.Sprintf(`No include block named %q exists in this file.`, attribute.Name)
		default:
			return nil
		}

		if _, exists := scope[attribute.Name]; !exists {
			result = append(result, semanticDiagnostic(source, ast.TraverseAttrIdentRange(attribute), message))
		}

		return nil
	})

	result = append(result, validateDependencies(filename, source, st)...)
	result = append(result, validateDuplicateLocals(source, body)...)
	sortDiagnostics(result)

	return result
}

// FilterParser removes only dependency-runtime false positives whose ranges
// are proven by the AST to belong to dependency traversals.
func FilterParser(st store.Store, input []protocol.Diagnostic) []protocol.Diagnostic {
	if st.AST == nil {
		return input
	}

	filtered := make([]protocol.Diagnostic, 0, len(input))
	for _, diagnostic := range input {
		if strings.Contains(diagnostic.Message, `There is no variable named "dependency".`) &&
			isDependencyTraversal(st, diagnostic.Range.Start) {
			continue
		}

		if diagnostic.Message == unknownValueDiagnostic &&
			isGenerateContentsDependencyOutput(st, diagnostic.Range.Start) {
			continue
		}

		filtered = append(filtered, diagnostic)
	}

	return filtered
}

// Sort orders diagnostics deterministically by range and message.
func Sort(input []protocol.Diagnostic) {
	sortDiagnostics(input)
}

func validateDependencies(filename, source string, st store.Store) []protocol.Diagnostic {
	nodes := make([]*ast.IndexedNode, 0, len(st.AST.Dependencies))
	for _, node := range st.AST.Dependencies {
		nodes = append(nodes, node)
	}

	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Range().Start.Byte < nodes[j].Range().Start.Byte
	})

	result := make([]protocol.Diagnostic, 0)

	for _, node := range nodes {
		block, ok := node.Node.(*hclsyntax.Block)
		if !ok || len(block.Labels) == 0 {
			continue
		}

		name := block.Labels[0]

		attribute, exists := block.Body.Attributes["config_path"]
		if !exists {
			result = append(result, semanticDiagnostic(source, block.TypeRange,
				fmt.Sprintf(`Dependency %q is missing a config_path attribute.`, name)))

			continue
		}

		configPath, err := terragruntpath.DependencyConfig(st, name)
		if err != nil {
			if hasParentFileNotFoundDiagnostic(st.Diagnostics) {
				continue
			}

			result = append(result, semanticDiagnostic(source, attribute.Expr.Range(),
				fmt.Sprintf(`Could not evaluate dependency %q config_path to a concrete string path.`, name)))

			continue
		}

		if _, err := terragruntpath.DependencyTarget(filename, configPath); err != nil {
			result = append(result, semanticDiagnostic(source, attribute.Expr.Range(),
				fmt.Sprintf(`Dependency %q points to %q, but no Terragrunt file was found there.`, name, configPath)))
		}
	}

	return result
}

func hasParentFileNotFoundDiagnostic(diagnostics []protocol.Diagnostic) bool {
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic.Message, parentFileNotFoundDiagnostic) {
			return true
		}
	}

	return false
}

func validateDuplicateLocals(source string, body *hclsyntax.Body) []protocol.Diagnostic {
	seen := false
	result := make([]protocol.Diagnostic, 0)

	for _, block := range body.Blocks {
		if block.Type != "locals" {
			continue
		}

		if !seen {
			seen = true
			continue
		}

		result = append(result, semanticDiagnostic(source, block.TypeRange, `Only one locals block is allowed per file.`))
	}

	return result
}

func semanticDiagnostic(source string, sourceRange hcl.Range, message string) protocol.Diagnostic {
	return protocol.Diagnostic{
		Range:    ast.FromHCLRange(source, sourceRange),
		Severity: protocol.DiagnosticSeverityError,
		Source:   sourceName,
		Message:  message,
	}
}

func isDependencyTraversal(st store.Store, position protocol.Position) bool {
	node := st.AST.FindNodeAt(ast.ToHCLPos(st.Document, position))
	for current := node; current != nil; current = current.Parent {
		expression, ok := current.Node.(*hclsyntax.ScopeTraversalExpr)
		if !ok || len(expression.Traversal) == 0 {
			continue
		}

		root, ok := expression.Traversal[0].(hcl.TraverseRoot)

		return ok && root.Name == "dependency"
	}

	return false
}

func isGenerateContentsDependencyOutput(st store.Store, position protocol.Position) bool {
	node := st.AST.FindNodeAt(ast.ToHCLPos(st.Document, position))

	attributeNode := ast.FindFirstParentMatch(node, func(candidate *ast.IndexedNode) bool {
		attribute, ok := candidate.Node.(*hclsyntax.Attribute)

		return ok && attribute.Name == "contents"
	})
	if attributeNode == nil {
		return false
	}

	generateBlock := ast.FindFirstParentMatch(attributeNode, func(candidate *ast.IndexedNode) bool {
		block, ok := candidate.Node.(*hclsyntax.Block)

		return ok && block.Type == "generate"
	})
	if generateBlock == nil {
		return false
	}

	attribute := attributeNode.Node.(*hclsyntax.Attribute)
	found := false
	_ = hclsyntax.VisitAll(attribute.Expr, func(node hclsyntax.Node) hcl.Diagnostics {
		expression, ok := node.(*hclsyntax.ScopeTraversalExpr)
		if !ok || len(expression.Traversal) < 3 {
			return nil
		}

		root, rootOK := expression.Traversal[0].(hcl.TraverseRoot)
		dependency, dependencyOK := expression.Traversal[1].(hcl.TraverseAttr)

		outputs, outputsOK := expression.Traversal[2].(hcl.TraverseAttr)
		if rootOK && dependencyOK && outputsOK &&
			root.Name == "dependency" &&
			outputs.Name == "outputs" {
			if _, exists := st.AST.Dependencies[dependency.Name]; exists {
				found = true
			}
		}

		return nil
	})

	return found
}

func sortDiagnostics(input []protocol.Diagnostic) {
	sort.SliceStable(input, func(i, j int) bool {
		if input[i].Range.Start.Line != input[j].Range.Start.Line {
			return input[i].Range.Start.Line < input[j].Range.Start.Line
		}

		if input[i].Range.Start.Character != input[j].Range.Start.Character {
			return input[i].Range.Start.Character < input[j].Range.Start.Character
		}

		return input[i].Message < input[j].Message
	})
}
