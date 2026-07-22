// Package path resolves filesystem targets referenced by Terragrunt syntax.
package path

import (
	"fmt"
	"os"
	"path/filepath"

	"terragrunt-ls/internal/ast"
	"terragrunt-ls/internal/tg/store"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// ResolutionError describes a path that could not be safely resolved.
type ResolutionError struct {
	Operation string
	Name      string
	Path      string
	Err       error
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

	var lastErr error = os.ErrNotExist
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
		return "", &ResolutionError{Operation: "file", Err: fmt.Errorf("missing path argument")}
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
	context := &hcl.EvalContext{Variables: map[string]cty.Value{}}
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

func stringValue(operation, name string, value cty.Value) (string, error) {
	if value == cty.NilVal || value.IsMarked() || !value.IsKnown() || value.IsNull() || value.Type() != cty.String {
		return "", &ResolutionError{Operation: operation, Name: name, Err: fmt.Errorf("value is not a known string")}
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
		return "", &ResolutionError{Operation: operation, Name: name, Path: target, Err: fmt.Errorf("not a regular file")}
	}

	return target, nil
}
