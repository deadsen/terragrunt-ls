// Package definition provides the logic for finding
// definitions in Terragrunt configurations.
package definition

import (
	"terragrunt-ls/internal/ast"
	"terragrunt-ls/internal/logger"
	terragruntpath "terragrunt-ls/internal/tg/path"
	"terragrunt-ls/internal/tg/store"
	"terragrunt-ls/internal/tg/symbol"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

const (
	// DefinitionContextLocal is the context for a local variable definition.
	// This means that the user is trying to find the definition of a `local.X`
	// reference, which resolves to a `locals { X = ... }` declaration in the
	// current file or a sibling file in the same module folder.
	DefinitionContextLocal = "local"

	// DefinitionContextInclude is the context for an include definition.
	// This means that the user is trying to find the definition of an include.
	DefinitionContextInclude = "include"

	// DefinitionContextDependency is the context for a dependency definition.
	// This means that the user is trying to find the definition of a dependency.
	DefinitionContextDependency = "dependency"

	// DefinitionContextNull is the context for a null definition.
	// This means that the user is trying to go to the definition of nothing useful.
	DefinitionContextNull = "null"
)

func GetDefinitionTargetWithContext(l logger.Logger, store store.Store, position protocol.Position) (string, string) {
	if store.AST == nil {
		l.Debug("No AST found")
		return "", DefinitionContextNull
	}

	node := store.AST.FindNodeAt(ast.ToHCLPos(store.Document, position))
	if node == nil {
		l.Debug("No node found at", "line", position.Line, "character", position.Character)
		return "", DefinitionContextNull
	}

	if include, ok := ast.GetNodeIncludeLabel(node); ok {
		l.Debug("Found include", "label", include)
		return include, DefinitionContextInclude
	}

	if dep, ok := ast.GetNodeDependencyLabel(node); ok {
		l.Debug("Found dependency", "label", dep)
		return dep, DefinitionContextDependency
	}

	if expr, ok := node.Node.(*hclsyntax.ScopeTraversalExpr); ok {
		if name, context, ok := traversalDefinitionTarget(expr); ok {
			l.Debug("Found traversal target", "name", name, "context", context)
			return name, context
		}
	}

	l.Debug("No definition found at", "line", position.Line, "character", position.Character)

	return "", DefinitionContextNull
}

// Resolve returns all definition locations for the target at position.
func Resolve(st store.Store, docURI protocol.DocumentURI, position protocol.Position) []protocol.Location {
	if st.AST == nil {
		return nil
	}

	if target, ok := symbol.At(st.AST, st.Document, position); ok {
		switch target.Kind {
		case symbol.Local:
			for _, occurrence := range symbol.Occurrences(st.AST, st.Document, target, true) {
				if occurrence.Declaration {
					return []protocol.Location{{URI: docURI, Range: occurrence.Range}}
				}
			}
			return nil
		case symbol.Dependency:
			configPath, err := terragruntpath.DependencyConfig(st, target.Name)
			if err != nil {
				return nil
			}
			targetFile, err := terragruntpath.DependencyTarget(docURI.Filename(), configPath)
			if err != nil {
				return nil
			}
			return fileLocation(targetFile)
		case symbol.Include:
			targetFile, err := terragruntpath.Include(st, target.Name)
			if err != nil {
				return nil
			}
			return fileLocation(targetFile)
		}
	}

	node := st.AST.FindNodeAt(ast.ToHCLPos(st.Document, position))
	if node == nil {
		return nil
	}
	if dependency, ok := ast.GetNodeDependencyLabel(node); ok {
		configPath, err := terragruntpath.DependencyConfig(st, dependency)
		if err == nil {
			if targetFile, targetErr := terragruntpath.DependencyTarget(docURI.Filename(), configPath); targetErr == nil {
				return fileLocation(targetFile)
			}
		}
	}
	if include, ok := ast.GetNodeIncludeLabel(node); ok {
		if targetFile, err := terragruntpath.Include(st, include); err == nil {
			return fileLocation(targetFile)
		}
	}
	if targetFile, err := terragruntpath.FileCall(st, node); err == nil {
		return fileLocation(targetFile)
	}

	return nil
}

func fileLocation(filename string) []protocol.Location {
	return []protocol.Location{{URI: uri.File(filename), Range: protocol.Range{}}}
}

// traversalDefinitionTarget extracts a (name, context) pair from a
// `local.<name>` traversal.
func traversalDefinitionTarget(expr *hclsyntax.ScopeTraversalExpr) (string, string, bool) {
	if len(expr.Traversal) < ast.MinReferenceTraversalLen {
		return "", "", false
	}

	rootStep, ok := expr.Traversal[0].(hcl.TraverseRoot)
	if !ok {
		return "", "", false
	}

	attrStep, ok := expr.Traversal[1].(hcl.TraverseAttr)
	if !ok {
		return "", "", false
	}

	if rootStep.Name == "local" {
		return attrStep.Name, DefinitionContextLocal, true
	}

	return "", "", false
}
