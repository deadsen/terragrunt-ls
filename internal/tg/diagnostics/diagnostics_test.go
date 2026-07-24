package diagnostics_test

import (
	"path/filepath"
	"testing"

	"terragrunt-ls/internal/ast"
	"terragrunt-ls/internal/tg/diagnostics"
	"terragrunt-ls/internal/tg/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zclconf/go-cty/cty"
	"go.lsp.dev/protocol"
)

func TestDiagnosticValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{"missing local", `inputs = { x = local.absent }`, `No local named "absent" exists in this file.`},
		{"missing dependency", `inputs = { x = dependency.absent.outputs.x }`, `No dependency block named "absent" exists in this file.`},
		{"missing include", `inputs = { x = include.absent.inputs.x }`, `No include block named "absent" exists in this file.`},
		{"missing config path", `dependency "app" {}`, `Dependency "app" is missing a config_path attribute.`},
		{"unevaluable config path", `dependency "app" { config_path = dependency.other.outputs.path }`, `Could not evaluate dependency "app" config_path to a concrete string path.`},
		{"missing dependency target", `dependency "app" { config_path = "../missing" }`, `Dependency "app" points to "../missing", but no Terragrunt file was found there.`},
		{"duplicate locals", "locals { a = 1 }\nlocals { b = 2 }", `Only one locals block is allowed per file.`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			filename := filepath.Join(t.TempDir(), "terragrunt.hcl")
			indexed, _ := ast.ParseHCLFile(filename, []byte(tt.source))
			st := store.Store{AST: indexed, Document: tt.source, CfgAsCty: cty.NilVal}

			result := diagnostics.Validate(filename, tt.source, st)

			require.NotEmpty(t, result)
			assert.Contains(t, diagnosticMessages(result), tt.want)
			for _, diagnostic := range result {
				assert.Equal(t, "Terragrunt", diagnostic.Source)
				assert.InDelta(t, float64(protocol.DiagnosticSeverityError), float64(diagnostic.Severity), 0)
			}
		})
	}
}

func TestDiagnosticValidationSkipsIncompleteTraversals(t *testing.T) {
	t.Parallel()

	for _, source := range []string{
		`inputs = { x = local. }`,
		`inputs = { x = dependency. }`,
		`inputs = { x = include. }`,
	} {
		indexed, _ := ast.ParseHCLFile("terragrunt.hcl", []byte(source))
		st := store.Store{AST: indexed, Document: source, CfgAsCty: cty.NilVal}
		assert.Empty(t, diagnostics.Validate("terragrunt.hcl", source, st))
	}
}

func TestDiagnosticFilterParserRequiresDependencyTraversal(t *testing.T) {
	t.Parallel()

	source := `inputs = {
  dependency_value = dependency.app.outputs.id
  unrelated_value  = other.app
}`
	indexed, err := ast.ParseHCLFile("terragrunt.hcl", []byte(source))
	require.NoError(t, err)
	st := store.Store{AST: indexed, Document: source}
	message := `Unknown variable: There is no variable named "dependency".`
	input := []protocol.Diagnostic{
		{
			Range:    protocol.Range{Start: protocol.Position{Line: 1, Character: 21}, End: protocol.Position{Line: 1, Character: 31}},
			Severity: protocol.DiagnosticSeverityError,
			Source:   "HCL",
			Message:  message,
		},
		{
			Range:    protocol.Range{Start: protocol.Position{Line: 2, Character: 21}, End: protocol.Position{Line: 2, Character: 26}},
			Severity: protocol.DiagnosticSeverityError,
			Source:   "HCL",
			Message:  message,
		},
	}

	filtered := diagnostics.FilterParser(st, input)

	require.Len(t, filtered, 1)
	assert.Equal(t, uint32(2), filtered[0].Range.Start.Line)
}

func TestDiagnosticFilterParserSkipsUnknownGenerateDependencyContents(t *testing.T) {
	t.Parallel()

	source := `dependency "zone" {
  config_path = "../zone"
}

generate "extra" {
  contents = <<EOF
locals {}
resource "aws_route53_record" "this" {
  zone_id = "${dependency.zone.outputs.zone_id}"
}
EOF
}`
	indexed, err := ast.ParseHCLFile("terragrunt.hcl", []byte(source))
	require.NoError(t, err)
	st := store.Store{AST: indexed, Document: source}
	message := `Unsuitable value type: Unsuitable value: value must be known`

	filtered := diagnostics.FilterParser(st, []protocol.Diagnostic{{
		Range: protocol.Range{
			Start: protocol.Position{Line: 6, Character: 0},
			End:   protocol.Position{Line: 8, Character: 13},
		},
		Severity: protocol.DiagnosticSeverityError,
		Source:   "HCL",
		Message:  message,
	}})

	assert.Empty(t, filtered)
}

func TestDiagnosticFilterParserRetainsUnknownValueOutsideGenerateContents(t *testing.T) {
	t.Parallel()

	source := `dependency "module" {
  config_path = "../module"
}

generate "extra" {
  path     = dependency.module.outputs.path
  contents = "content"
}`
	indexed, err := ast.ParseHCLFile("terragrunt.hcl", []byte(source))
	require.NoError(t, err)
	st := store.Store{AST: indexed, Document: source}
	message := `Unsuitable value type: Unsuitable value: value must be known`
	input := []protocol.Diagnostic{{
		Range: protocol.Range{
			Start: protocol.Position{Line: 5, Character: 13},
			End:   protocol.Position{Line: 5, Character: 43},
		},
		Severity: protocol.DiagnosticSeverityError,
		Source:   "HCL",
		Message:  message,
	}}

	filtered := diagnostics.FilterParser(st, input)

	assert.Equal(t, input, filtered)
}

func diagnosticMessages(input []protocol.Diagnostic) []string {
	result := make([]string, 0, len(input))
	for _, diagnostic := range input {
		result = append(result, diagnostic.Message)
	}
	return result
}
