// Package path resolves filesystem targets referenced by Terragrunt syntax.
package path

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"terragrunt-ls/internal/ast"
	"terragrunt-ls/internal/tg/store"

	"github.com/gruntwork-io/terragrunt/pkg/config"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

const (
	maxFindInParentFoldersArgs = 2
	maxParentFolders           = 100
)

// ResolutionError describes a path that could not be safely resolved.
type ResolutionError struct {
	Err       error
	Operation string
	Name      string
	Path      string
}

func (e *ResolutionError) Error() string {
	return fmt.Sprintf("%s %q path %q: %v", e.Operation, e.Name, e.Path, e.Err)
}

func (e *ResolutionError) Unwrap() error {
	return e.Err
}

// DependencyConfig returns the evaluated config_path for a dependency.
func DependencyConfig(st store.Store, name string) (string, error) {
	if st.Cfg != nil {
		for _, dependency := range st.Cfg.TerragruntDependencies {
			if dependency.Name != name {
				continue
			}

			return stringValue("dependency", name, dependency.ConfigPath)
		}
	}

	if st.AST != nil {
		if node, ok := st.AST.Dependencies[name]; ok {
			block, blockOK := node.Node.(*hclsyntax.Block)
			if blockOK {
				if attribute, attrOK := block.Body.Attributes["config_path"]; attrOK {
					value, diags := attribute.Expr.Value(localEvalContext(st))
					if diags.HasErrors() {
						return "", &ResolutionError{Operation: "dependency", Name: name, Err: diags}
					}

					return stringValue("dependency", name, value)
				}
			}
		}
	}

	return "", &ResolutionError{Operation: "dependency", Name: name, Err: os.ErrNotExist}
}

// DependencyTarget resolves a dependency config path relative to sourceFile.
func DependencyTarget(sourceFile, configPath string) (string, error) {
	target := configPath
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(sourceFile), target)
	}

	target = filepath.Clean(target)

	if info, err := os.Stat(target); err == nil && info.Mode().IsRegular() {
		return target, nil
	}

	var lastErr = os.ErrNotExist

	for _, name := range []string{"terragrunt.hcl", "terragrunt.hcl.json"} {
		candidate := filepath.Join(target, name)

		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() {
			return candidate, nil
		}

		if err != nil {
			lastErr = err
		}
	}

	return "", &ResolutionError{Operation: "dependency target", Path: configPath, Err: lastErr}
}

// Include returns the evaluated target path for an include block.
func Include(st store.Store, name string) (string, error) {
	if st.Cfg != nil {
		if include, ok := st.Cfg.ProcessedIncludes[name]; ok && include.Path != "" {
			return include.Path, nil
		}
	}

	if st.AST != nil {
		if node, ok := st.AST.Includes[name]; ok {
			block, blockOK := node.Node.(*hclsyntax.Block)
			if blockOK {
				if attribute, attrOK := block.Body.Attributes["path"]; attrOK {
					value, diags := attribute.Expr.Value(localEvalContext(st))
					if diags.HasErrors() {
						return "", &ResolutionError{Operation: "include", Name: name, Err: diags}
					}

					includePath, err := stringValue("include", name, value)
					if err != nil {
						return "", err
					}

					return regularFile(sourceFilename(st), includePath, "include", name)
				}
			}
		}
	}

	return "", &ResolutionError{Operation: "include", Name: name, Err: os.ErrNotExist}
}

// FileCall resolves the first string argument of the enclosing file(...) call.
// Its restricted evaluation context supports locals and find_in_parent_folders
// without enabling functions that can execute commands or access the network.
func FileCall(st store.Store, node *ast.IndexedNode) (string, error) {
	callNode := ast.FindFirstParentMatch(node, func(candidate *ast.IndexedNode) bool {
		call, ok := candidate.Node.(*hclsyntax.FunctionCallExpr)
		return ok && call.Name == "file"
	})
	if callNode == nil {
		return "", &ResolutionError{Operation: "file", Err: os.ErrNotExist}
	}

	call := callNode.Node.(*hclsyntax.FunctionCallExpr)
	if len(call.Args) == 0 {
		return "", &ResolutionError{Operation: "file", Err: errors.New("missing path argument")}
	}

	value, diags := call.Args[0].Value(localEvalContext(st))
	if diags.HasErrors() {
		return "", &ResolutionError{Operation: "file", Err: diags}
	}

	filePath, err := stringValue("file", "", value)
	if err != nil {
		return "", err
	}

	return regularFile(sourceFilename(st), filePath, "file", "")
}

func localEvalContext(st store.Store) *hcl.EvalContext {
	context := &hcl.EvalContext{
		Functions: map[string]function.Function{
			config.FuncNameFindInParentFolders: findInParentFoldersFunction(sourceFilename(st)),
		},
		Variables: map[string]cty.Value{},
	}
	if st.CfgAsCty == cty.NilVal || st.CfgAsCty.IsMarked() || !st.CfgAsCty.IsKnown() || st.CfgAsCty.IsNull() {
		return context
	}

	if !st.CfgAsCty.Type().HasAttribute("locals") {
		return context
	}

	locals := st.CfgAsCty.GetAttr("locals")
	if locals.IsMarked() || !locals.IsKnown() || locals.IsNull() {
		return context
	}

	context.Variables["local"] = locals

	return context
}

func findInParentFoldersFunction(sourceFile string) function.Function {
	return function.New(&function.Spec{
		VarParam: &function.Parameter{Type: cty.String},
		Type:     function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			if len(args) > maxFindInParentFoldersArgs {
				return cty.NilVal, errors.New("find_in_parent_folders expects zero, one, or two arguments")
			}

			params := make([]string, 0, len(args))
			for _, arg := range args {
				if !arg.IsKnown() || arg.IsNull() {
					return cty.NilVal, errors.New("find_in_parent_folders arguments must be known strings")
				}

				params = append(params, arg.AsString())
			}

			resolved, err := findInParentFolders(sourceFile, params)
			if err != nil {
				return cty.NilVal, err
			}

			return cty.StringVal(resolved), nil
		},
	})
}

func findInParentFolders(sourceFile string, params []string) (string, error) {
	filename := "terragrunt.hcl"
	if len(params) > 0 && params[0] != "" {
		filename = params[0]
	}

	currentDir, err := filepath.Abs(filepath.Dir(sourceFile))
	if err != nil {
		return "", err
	}

	for range maxParentFolders {
		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			if len(params) == maxFindInParentFoldersArgs {
				return params[1], nil
			}

			return "", fmt.Errorf("find_in_parent_folders could not find %q", filename)
		}

		candidate := filepath.Join(parentDir, filename)
		if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().IsRegular() {
			return candidate, nil
		}

		currentDir = parentDir
	}

	return "", fmt.Errorf("find_in_parent_folders exceeded %d parent directories", maxParentFolders)
}

func stringValue(operation, name string, value cty.Value) (string, error) {
	if value == cty.NilVal || value.IsMarked() || !value.IsKnown() || value.IsNull() || value.Type() != cty.String {
		return "", &ResolutionError{Operation: operation, Name: name, Err: errors.New("value is not a known string")}
	}

	return value.AsString(), nil
}

func sourceFilename(st store.Store) string {
	if st.AST == nil || st.AST.HCLFile == nil {
		return ""
	}

	if body, ok := st.AST.HCLFile.Body.(*hclsyntax.Body); ok {
		return body.SrcRange.Filename
	}

	return ""
}

func regularFile(sourceFile, target, operation, name string) (string, error) {
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(sourceFile), target)
	}

	target = filepath.Clean(target)

	info, err := os.Stat(target)
	if err != nil {
		return "", &ResolutionError{Operation: operation, Name: name, Path: target, Err: err}
	}

	if !info.Mode().IsRegular() {
		return "", &ResolutionError{Operation: operation, Name: name, Path: target, Err: errors.New("not a regular file")}
	}

	return target, nil
}
