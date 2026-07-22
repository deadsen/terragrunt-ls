package tg

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"terragrunt-ls/internal/ast"
	"terragrunt-ls/internal/logger"
	"terragrunt-ls/internal/lsp"
	"terragrunt-ls/internal/tg/completion"
	"terragrunt-ls/internal/tg/definition"
	"terragrunt-ls/internal/tg/diagnostics"
	"terragrunt-ls/internal/tg/hover"
	"terragrunt-ls/internal/tg/references"
	"terragrunt-ls/internal/tg/rename"
	"terragrunt-ls/internal/tg/store"
	"terragrunt-ls/internal/tg/symbol"
	"terragrunt-ls/internal/tg/text"

	"github.com/gruntwork-io/terragrunt/pkg/config"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
	"go.lsp.dev/protocol"
)

type State struct {
	// Map of file names to Terragrunt configs
	mu          sync.RWMutex
	Configs     map[string]store.Store
	generations map[string]uint64
}

func NewState() *State {
	return &State{
		Configs:     map[string]store.Store{},
		generations: map[string]uint64{},
	}
}

func (s *State) OpenDocument(ctx context.Context, l logger.Logger, docURI protocol.DocumentURI, text string, version int32) []protocol.Diagnostic {
	diagnostics, _ := s.OpenDocumentWithStatus(ctx, l, docURI, text, version)
	return diagnostics
}

func (s *State) OpenDocumentWithStatus(ctx context.Context, l logger.Logger, docURI protocol.DocumentURI, text string, version int32) ([]protocol.Diagnostic, bool) {
	generation := s.generation(docURI)

	l.Debug(
		"Opening document",
		"uri", docURI,
		"text", text,
	)

	return s.updateStateAtGeneration(ctx, l, docURI, text, version, generation)
}

func (s *State) UpdateDocument(ctx context.Context, l logger.Logger, docURI protocol.DocumentURI, text string, version int32) []protocol.Diagnostic {
	diagnostics, _ := s.UpdateDocumentWithStatus(ctx, l, docURI, text, version)
	return diagnostics
}

func (s *State) UpdateDocumentWithStatus(ctx context.Context, l logger.Logger, docURI protocol.DocumentURI, text string, version int32) ([]protocol.Diagnostic, bool) {
	generation := s.generation(docURI)

	l.Debug(
		"Updating document",
		"uri", docURI,
		"text", text,
	)

	return s.updateStateAtGeneration(ctx, l, docURI, text, version, generation)
}

func (s *State) SaveDocument(ctx context.Context, l logger.Logger, docURI protocol.DocumentURI) []protocol.Diagnostic {
	diagnostics, _ := s.SaveDocumentWithStatus(ctx, l, docURI)
	return diagnostics
}

func (s *State) SaveDocumentWithStatus(ctx context.Context, l logger.Logger, docURI protocol.DocumentURI) ([]protocol.Diagnostic, bool) {
	filename := docURI.Filename()

	s.mu.RLock()
	st, ok := s.Configs[filename]
	generation := s.generations[filename]
	s.mu.RUnlock()
	if !ok {
		return []protocol.Diagnostic{}, false
	}

	return s.updateStateAtGeneration(ctx, l, docURI, st.Document, st.Version, generation)
}

func (s *State) CloseDocument(docURI protocol.DocumentURI) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.generations[docURI.Filename()]++
	delete(s.Configs, docURI.Filename())
}

func (s *State) Document(docURI protocol.DocumentURI) (store.Store, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	st, ok := s.Configs[docURI.Filename()]
	return st, ok
}

func (s *State) IsCurrent(docURI protocol.DocumentURI, version int32) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	st, ok := s.Configs[docURI.Filename()]
	return ok && st.Version == version
}

func (s *State) generation(docURI protocol.DocumentURI) uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.generations[docURI.Filename()]
}

func (s *State) updateStateAtGeneration(ctx context.Context, l logger.Logger, docURI protocol.DocumentURI, text string, version int32, generation uint64) ([]protocol.Diagnostic, bool) {
	filename := docURI.Filename()

	s.mu.RLock()
	currentGeneration := s.generations[filename]
	current, ok := s.Configs[filename]
	s.mu.RUnlock()
	if generation != currentGeneration {
		return []protocol.Diagnostic{}, false
	}
	if ok && version < current.Version {
		return []protocol.Diagnostic{}, false
	}

	fileType := DetectFileType(filename)

	// Ignore errors from AST indexing since we'll get the same errors from the Terragrunt parser just below
	indexedAST, _ := ast.ParseHCLFile(filename, []byte(text))

	st := store.Store{
		AST:      indexedAST,
		CfgAsCty: cty.NilVal,
		Document: text,
		FileType: fileType,
		Version:  version,
	}

	var diags []protocol.Diagnostic

	switch fileType {
	case store.FileTypeUnit:
		cfg, unitDiags := ParseTerragruntBuffer(ctx, l, filename, text)

		l.Debug(
			"Config",
			"uri", docURI,
			"config", cfg,
		)

		cfgAsCty := cty.NilVal

		if cfg != nil {
			if converted, err := config.TerragruntConfigAsCty(cfg); err == nil {
				cfgAsCty = converted
			}
		}

		st.Cfg = cfg
		st.CfgAsCty = cfgAsCty
		diags = unitDiags

	case store.FileTypeStack:
		stackCfg, stackDiags := ParseStackBuffer(ctx, l, filename, text)

		l.Debug(
			"Stack Config",
			"uri", docURI,
			"config", stackCfg,
		)

		st.StackCfg = stackCfg
		diags = stackDiags

	case store.FileTypeValues:
		// Values files are generated; only store the document for formatting.
		diags = []protocol.Diagnostic{}

	case store.FileTypeUnknown:
		diags = []protocol.Diagnostic{}
	}

	if fileType == store.FileTypeUnit {
		diags = diagnostics.FilterParser(st, diags)
		diags = append(diags, diagnostics.Validate(filename, text, st)...)
		diagnostics.Sort(diags)
	}
	st.Diagnostics = append([]protocol.Diagnostic(nil), diags...)

	s.mu.Lock()
	defer s.mu.Unlock()

	if generation != s.generations[filename] {
		return []protocol.Diagnostic{}, false
	}

	current, ok = s.Configs[filename]
	if ok && version < current.Version {
		return []protocol.Diagnostic{}, false
	}

	s.Configs[filename] = st

	return diags, true
}

func (s *State) Hover(l logger.Logger, id int, docURI protocol.DocumentURI, position protocol.Position) lsp.HoverResponse {
	st, ok := s.Document(docURI)
	if !ok {
		return newEmptyHoverResponse(id)
	}

	l.Debug(
		"Hovering over character",
		"uri", docURI,
		"position", position,
	)

	if st.FileType != store.FileTypeUnit {
		return newEmptyHoverResponse(id)
	}

	l.Debug(
		"Config",
		"uri", docURI,
		"config", st.Cfg,
	)

	path, _, found := hover.GetLocalPath(st, position)

	l.Debug(
		"Hovering with context",
		"path", path,
	)

	if !found || len(path) == 0 {
		return newEmptyHoverResponse(id)
	}

	if st.Cfg == nil {
		return newEmptyHoverResponse(id)
	}
	if _, ok := st.Cfg.Locals[path[0]]; !ok {
		return newEmptyHoverResponse(id)
	}
	if st.CfgAsCty == cty.NilVal || st.CfgAsCty.IsMarked() || !st.CfgAsCty.IsKnown() || st.CfgAsCty.IsNull() {
		return newEmptyHoverResponse(id)
	}

	locals, ok := localValue(st.CfgAsCty, []string{"locals"})
	if !ok {
		return newEmptyHoverResponse(id)
	}
	localVal, ok := localValue(locals, path)
	if !ok {
		return newEmptyHoverResponse(id)
	}

	name := path[len(path)-1]
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()
	rootBody.SetAttributeValue(name, localVal)

	return lsp.HoverResponse{
		Response: lsp.Response{
			RPC: lsp.RPCVersion,
			ID:  &id,
		},
		Result: lsp.HoverResult{
			Contents: protocol.MarkupContent{
				Kind:  protocol.Markdown,
				Value: text.WrapAsHCLCodeFence(strings.TrimSpace(string(f.Bytes()))),
			},
		},
	}
}

func localValue(value cty.Value, path []string) (cty.Value, bool) {
	for _, name := range path {
		if value == cty.NilVal || value.IsMarked() || !value.IsKnown() || value.IsNull() {
			return cty.NilVal, false
		}

		switch {
		case value.Type().HasAttribute(name):
			value = value.GetAttr(name)
		case value.Type().IsMapType() && value.HasIndex(cty.StringVal(name)).True():
			value = value.Index(cty.StringVal(name))
		default:
			return cty.NilVal, false
		}
	}

	return value, value != cty.NilVal && !value.IsMarked() && value.IsKnown() && !value.IsNull()
}

func newEmptyHoverResponse(id int) lsp.HoverResponse {
	return lsp.HoverResponse{
		Response: lsp.Response{
			RPC: lsp.RPCVersion,
			ID:  &id,
		},
	}
}

func (s *State) Definition(_ logger.Logger, id int, docURI protocol.DocumentURI, position protocol.Position) lsp.DefinitionResponse {
	st, ok := s.Document(docURI)
	if !ok {
		return lsp.DefinitionResponse{
			Response: lsp.Response{RPC: lsp.RPCVersion, ID: &id},
			Result:   []protocol.Location{},
		}
	}

	locations := definition.Resolve(st, docURI, position)
	if locations == nil {
		locations = []protocol.Location{}
	}

	return lsp.DefinitionResponse{
		Response: lsp.Response{RPC: lsp.RPCVersion, ID: &id},
		Result:   locations,
	}
}

func (s *State) TextDocumentCompletion(l logger.Logger, id int, docURI protocol.DocumentURI, position protocol.Position) lsp.CompletionResponse {
	st, ok := s.Document(docURI)
	if !ok {
		return lsp.CompletionResponse{
			Response: lsp.Response{RPC: lsp.RPCVersion, ID: &id},
			Result:   []protocol.CompletionItem{},
		}
	}

	items := completion.GetCompletions(l, st, position)

	response := lsp.CompletionResponse{
		Response: lsp.Response{
			RPC: "2.0",
			ID:  &id,
		},
		Result: items,
	}

	return response
}

func (s *State) TextDocumentFormatting(l logger.Logger, id int, docURI protocol.DocumentURI) lsp.FormatResponse {
	st, ok := s.Document(docURI)
	if !ok {
		return lsp.FormatResponse{
			Response: lsp.Response{RPC: lsp.RPCVersion, ID: &id},
			Result:   []protocol.TextEdit{},
		}
	}

	l.Debug(
		"Formatting requested",
		"uri", docURI,
	)

	formatted := hclwrite.Format([]byte(st.Document))

	return lsp.FormatResponse{
		Response: lsp.Response{
			RPC: lsp.RPCVersion,
			ID:  &id,
		},
		Result: []protocol.TextEdit{
			{
				Range: protocol.Range{
					Start: protocol.Position{
						Line:      0,
						Character: 0,
					},
					End: getEndOfDocument(st.Document),
				},
				NewText: string(formatted),
			},
		},
	}
}

func (s *State) PrepareRename(l logger.Logger, id int, docURI protocol.DocumentURI, position protocol.Position) lsp.PrepareRenameResponse {
	empty := lsp.PrepareRenameResponse{
		Response: lsp.Response{RPC: lsp.RPCVersion, ID: &id},
		Result:   nil,
	}

	st, ok := s.Document(docURI)
	if !ok || !canRename(st) {
		return empty
	}

	target := rename.GetRenameTarget(l, st, position)
	if target.Kind == "" {
		return empty
	}

	return lsp.PrepareRenameResponse{
		Response: lsp.Response{RPC: lsp.RPCVersion, ID: &id},
		Result: &lsp.PrepareRenameResult{
			Range:       target.IdentRange,
			Placeholder: target.Name,
		},
	}
}

func (s *State) TextDocumentRename(l logger.Logger, id int, docURI protocol.DocumentURI, position protocol.Position, newName string) lsp.RenameResponse {
	empty := lsp.RenameResponse{
		Response: lsp.Response{RPC: lsp.RPCVersion, ID: &id},
		Result:   nil,
	}

	st, ok := s.Document(docURI)
	if !ok || !canRename(st) {
		return empty
	}

	if !rename.IsValidIdentifier(newName) {
		l.Debug(
			"Rejecting invalid identifier",
			"newName", newName,
		)

		return empty
	}

	target := rename.GetRenameTarget(l, st, position)
	if target.Kind == "" {
		return empty
	}

	occurrences := rename.FindAllOccurrences(target, docURI.Filename(), st)
	if len(occurrences) == 0 {
		return empty
	}

	edits := make([]protocol.TextEdit, 0, len(occurrences))

	for _, occ := range occurrences {
		newText := newName
		if occ.IsDefinition && (target.Kind == symbol.Dependency || target.Kind == symbol.Include) {
			newText = strconv.Quote(newName)
		}
		edits = append(edits, protocol.TextEdit{
			Range:   occ.Range,
			NewText: newText,
		})
	}

	return lsp.RenameResponse{
		Response: lsp.Response{RPC: lsp.RPCVersion, ID: &id},
		Result: &protocol.WorkspaceEdit{
			Changes: map[protocol.DocumentURI][]protocol.TextEdit{docURI: edits},
		},
	}
}

// canRename reports whether rename can run against this store. It accepts any
// HCL config or auxiliary file (e.g., common.hcl) but not stack/values files,
// whose syntax does not have the renameable `local`/`include`/`dependency` constructs.
func canRename(st store.Store) bool {
	if st.AST == nil {
		return false
	}

	return st.FileType == store.FileTypeUnit || st.FileType == store.FileTypeUnknown
}

func (s *State) TextDocumentReferences(l logger.Logger, id int, docURI protocol.DocumentURI, position protocol.Position, includeDeclaration bool) lsp.ReferencesResponse {
	empty := lsp.ReferencesResponse{
		Response: lsp.Response{RPC: lsp.RPCVersion, ID: &id},
		Result:   nil,
	}

	st, ok := s.Document(docURI)
	if !ok || !canRename(st) {
		return empty
	}

	locations := references.GetReferences(l, st, position, docURI.Filename(), includeDeclaration)
	if len(locations) == 0 {
		return empty
	}

	return lsp.ReferencesResponse{
		Response: lsp.Response{RPC: lsp.RPCVersion, ID: &id},
		Result:   locations,
	}
}

func getEndOfDocument(doc string) protocol.Position {
	lines := strings.Split(doc, "\n")

	return protocol.Position{
		Line:      uint32(len(lines) - 1),
		Character: uint32(len(lines[len(lines)-1])),
	}
}
