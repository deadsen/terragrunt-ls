package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"

	"terragrunt-ls/internal/ast"
	"terragrunt-ls/internal/tg/symbol"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

const ResolveDependencyOutputsCommand = "terragrunt.resolveDependencyOutputs"

var unsafeTempNameCharacter = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

type dependencyOutputArgs struct {
	URI        protocol.DocumentURI `json:"uri"`
	Dependency string               `json:"dependency"`
}

func (s *Server) dependencyOutputCodeActions(params protocol.CodeActionParams) []protocol.CodeAction {
	st, ok := s.state.Document(params.TextDocument.URI)
	if !ok || st.AST == nil {
		return []protocol.CodeAction{}
	}

	name := ""
	if target, found := symbol.At(st.AST, st.Document, params.Range.Start); found && target.Kind == symbol.Dependency {
		name = target.Name
	} else {
		node := st.AST.FindNodeAt(ast.ToHCLPos(st.Document, params.Range.Start))
		for current := node; current != nil && name == ""; current = current.Parent {
			expression, expressionOK := current.Node.(*hclsyntax.ScopeTraversalExpr)
			if !expressionOK || len(expression.Traversal) < ast.MinReferenceTraversalLen {
				continue
			}
			root, rootOK := expression.Traversal[0].(hcl.TraverseRoot)
			attribute, attributeOK := expression.Traversal[1].(hcl.TraverseAttr)
			if rootOK && attributeOK && root.Name == string(symbol.Dependency) {
				name = attribute.Name
			}
		}

		dependencyBlock := ast.FindFirstParentMatch(node, ast.IsDependencyBlock)
		if name == "" && dependencyBlock != nil {
			block := dependencyBlock.Node.(*hclsyntax.Block)
			if len(block.Labels) > 0 {
				name = block.Labels[0]
			}
		}
	}
	if name == "" {
		return []protocol.CodeAction{}
	}

	args := dependencyOutputArgs{URI: params.TextDocument.URI, Dependency: name}
	return []protocol.CodeAction{{
		Title: fmt.Sprintf("Resolve outputs for dependency %q", name),
		Kind:  protocol.RefactorRewrite,
		Command: &protocol.Command{
			Title:     "Resolve Terragrunt dependency outputs",
			Command:   ResolveDependencyOutputsCommand,
			Arguments: []interface{}{args},
		},
	}}
}

func (s *Server) executeDependencyOutputs(ctx context.Context, args dependencyOutputArgs) (any, error) {
	st, ok := s.state.Document(args.URI)
	if !ok {
		return nil, fmt.Errorf("document is not open: %s", args.URI)
	}

	output, err := s.dependencyRunner.Resolve(ctx, args.URI.Filename(), args.Dependency, st)
	if err != nil {
		return nil, err
	}

	safeName := unsafeTempNameCharacter.ReplaceAllString(args.Dependency, "-")
	if safeName == "" {
		safeName = "dependency"
	}
	file, err := os.CreateTemp("", "terragrunt-"+safeName+"-outputs-*.json")
	if err != nil {
		return nil, fmt.Errorf("create dependency output file: %w", err)
	}
	filename := file.Name()
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(filename)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return nil, fmt.Errorf("secure dependency output file: %w", err)
	}
	if _, err := file.Write(output.JSON); err != nil {
		return nil, fmt.Errorf("write dependency output file: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close dependency output file: %w", err)
	}

	s.trackTempFile(filename)
	keep = true
	showResult, showErr := s.client.ShowDocument(ctx, protocol.ShowDocumentParams{
		URI:       uri.File(filename),
		TakeFocus: true,
	})
	if showErr != nil || showResult == nil || !showResult.Success {
		message := fmt.Sprintf("Terragrunt dependency outputs were written to %s", filename)
		if err := s.client.ShowMessage(ctx, protocol.ShowMessageParams{Type: protocol.MessageTypeInfo, Message: message}); err != nil {
			s.log.Warn("Could not show dependency output path", "path", filename, "error", err)
		}
	}

	return filename, nil
}

func decodeDependencyOutputArgs(arguments []interface{}) (dependencyOutputArgs, error) {
	if len(arguments) != 1 {
		return dependencyOutputArgs{}, fmt.Errorf("expected one dependency output argument")
	}
	encoded, err := json.Marshal(arguments[0])
	if err != nil {
		return dependencyOutputArgs{}, fmt.Errorf("encode dependency output argument: %w", err)
	}
	var args dependencyOutputArgs
	if err := json.Unmarshal(encoded, &args); err != nil {
		return dependencyOutputArgs{}, fmt.Errorf("decode dependency output argument: %w", err)
	}
	if args.URI == "" || args.Dependency == "" {
		return dependencyOutputArgs{}, fmt.Errorf("dependency output URI and dependency are required")
	}

	return args, nil
}

func (s *Server) trackTempFile(filename string) {
	s.tempMu.Lock()
	defer s.tempMu.Unlock()
	s.tempFiles[filename] = struct{}{}
}

func (s *Server) cleanupTempFiles() {
	s.tempMu.Lock()
	files := s.tempFiles
	s.tempFiles = make(map[string]struct{})
	s.tempMu.Unlock()
	for filename := range files {
		if err := os.Remove(filename); err != nil && !os.IsNotExist(err) {
			s.log.Warn("Could not remove dependency output file", "path", filename, "error", err)
		}
	}
}
