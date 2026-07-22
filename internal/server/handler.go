package server

import (
	"context"
	"encoding/json"
	"fmt"
	"terragrunt-ls/internal/lsp"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
)

func (s *Server) Handler() jsonrpc2.Handler {
	return func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		respond := func(result any, err error) error {
			return reply(ctx, result, err)
		}

		if s.shutdown.Load() && req.Method() != protocol.MethodExit {
			return respond(nil, fmt.Errorf("server has shut down: %w", jsonrpc2.ErrInvalidRequest))
		}

		switch req.Method() {
		case protocol.MethodInitialize:
			var params protocol.InitializeParams
			if err := decodeParams(req, &params); err != nil {
				return respond(nil, err)
			}
			return respond(s.initialize(), nil)

		case protocol.MethodInitialized:
			var params protocol.InitializedParams
			if err := decodeParams(req, &params); err != nil {
				return respond(nil, err)
			}
			return respond(nil, nil)

		case protocol.MethodTextDocumentDidOpen:
			var params protocol.DidOpenTextDocumentParams
			if err := decodeParams(req, &params); err != nil {
				return respond(nil, err)
			}
			diagnostics, applied := s.state.OpenDocumentWithStatus(ctx, s.log, params.TextDocument.URI, params.TextDocument.Text, params.TextDocument.Version)
			if !applied {
				return respond(nil, nil)
			}
			return respond(nil, s.client.PublishDiagnostics(ctx, protocol.PublishDiagnosticsParams{
				URI:         params.TextDocument.URI,
				Diagnostics: diagnostics,
			}))

		case protocol.MethodTextDocumentDidChange:
			var params protocol.DidChangeTextDocumentParams
			if err := decodeParams(req, &params); err != nil {
				return respond(nil, err)
			}
			if len(params.ContentChanges) == 0 {
				return respond(nil, nil)
			}
			change := params.ContentChanges[len(params.ContentChanges)-1]
			diagnostics, applied := s.state.UpdateDocumentWithStatus(ctx, s.log, params.TextDocument.URI, change.Text, params.TextDocument.Version)
			if !applied {
				return respond(nil, nil)
			}
			return respond(nil, s.client.PublishDiagnostics(ctx, protocol.PublishDiagnosticsParams{
				URI:         params.TextDocument.URI,
				Diagnostics: diagnostics,
			}))

		case protocol.MethodTextDocumentDidSave:
			var params protocol.DidSaveTextDocumentParams
			if err := decodeParams(req, &params); err != nil {
				return respond(nil, err)
			}
			diagnostics, applied := s.state.SaveDocumentWithStatus(ctx, s.log, params.TextDocument.URI)
			if !applied {
				return respond(nil, nil)
			}
			return respond(nil, s.client.PublishDiagnostics(ctx, protocol.PublishDiagnosticsParams{
				URI:         params.TextDocument.URI,
				Diagnostics: diagnostics,
			}))

		case protocol.MethodTextDocumentDidClose:
			var params protocol.DidCloseTextDocumentParams
			if err := decodeParams(req, &params); err != nil {
				return respond(nil, err)
			}
			s.state.CloseDocument(params.TextDocument.URI)
			err := s.client.PublishDiagnostics(ctx, protocol.PublishDiagnosticsParams{
				URI:         params.TextDocument.URI,
				Diagnostics: []protocol.Diagnostic{},
			})
			return respond(nil, err)

		case protocol.MethodTextDocumentHover:
			var params protocol.HoverParams
			if err := decodeParams(req, &params); err != nil {
				return respond(nil, err)
			}
			st, ok := s.state.Document(params.TextDocument.URI)
			result := s.state.Hover(s.log, 0, params.TextDocument.URI, params.Position).Result
			if !ok || !s.state.IsCurrent(params.TextDocument.URI, st.Version) {
				return respond(lsp.HoverResult{}, nil)
			}
			return respond(result, nil)

		case protocol.MethodTextDocumentDefinition:
			var params protocol.DefinitionParams
			if err := decodeParams(req, &params); err != nil {
				return respond(nil, err)
			}
			st, ok := s.state.Document(params.TextDocument.URI)
			result := s.state.Definition(s.log, 0, params.TextDocument.URI, params.Position).Result
			if !ok || !s.state.IsCurrent(params.TextDocument.URI, st.Version) {
				return respond(protocol.Location{
					URI: params.TextDocument.URI,
					Range: protocol.Range{
						Start: params.Position,
						End:   params.Position,
					},
				}, nil)
			}
			return respond(result, nil)

		case protocol.MethodTextDocumentCompletion:
			var params protocol.CompletionParams
			if err := decodeParams(req, &params); err != nil {
				return respond(nil, err)
			}
			st, ok := s.state.Document(params.TextDocument.URI)
			result := s.state.TextDocumentCompletion(s.log, 0, params.TextDocument.URI, params.Position).Result
			if !ok || !s.state.IsCurrent(params.TextDocument.URI, st.Version) {
				return respond([]protocol.CompletionItem{}, nil)
			}
			return respond(result, nil)

		case protocol.MethodTextDocumentFormatting:
			var params protocol.DocumentFormattingParams
			if err := decodeParams(req, &params); err != nil {
				return respond(nil, err)
			}
			st, ok := s.state.Document(params.TextDocument.URI)
			result := s.state.TextDocumentFormatting(s.log, 0, params.TextDocument.URI).Result
			if !ok || !s.state.IsCurrent(params.TextDocument.URI, st.Version) {
				return respond([]protocol.TextEdit{}, nil)
			}
			return respond(result, nil)

		case protocol.MethodTextDocumentReferences:
			var params protocol.ReferenceParams
			if err := decodeParams(req, &params); err != nil {
				return respond(nil, err)
			}
			st, ok := s.state.Document(params.TextDocument.URI)
			result := s.state.TextDocumentReferences(s.log, 0, params.TextDocument.URI, params.Position, params.Context.IncludeDeclaration).Result
			if !ok || !s.state.IsCurrent(params.TextDocument.URI, st.Version) {
				return respond(nil, nil)
			}
			return respond(result, nil)

		case protocol.MethodTextDocumentPrepareRename:
			var params protocol.PrepareRenameParams
			if err := decodeParams(req, &params); err != nil {
				return respond(nil, err)
			}
			st, ok := s.state.Document(params.TextDocument.URI)
			result := s.state.PrepareRename(s.log, 0, params.TextDocument.URI, params.Position).Result
			if !ok || !s.state.IsCurrent(params.TextDocument.URI, st.Version) {
				return respond(nil, nil)
			}
			return respond(result, nil)

		case protocol.MethodTextDocumentRename:
			var params protocol.RenameParams
			if err := decodeParams(req, &params); err != nil {
				return respond(nil, err)
			}
			st, ok := s.state.Document(params.TextDocument.URI)
			result := s.state.TextDocumentRename(s.log, 0, params.TextDocument.URI, params.Position, params.NewName).Result
			if !ok || !s.state.IsCurrent(params.TextDocument.URI, st.Version) {
				return respond(nil, nil)
			}
			return respond(result, nil)

		case protocol.MethodTextDocumentCodeAction:
			var params protocol.CodeActionParams
			if err := decodeParams(req, &params); err != nil {
				return respond(nil, err)
			}
			return respond(s.dependencyOutputCodeActions(params), nil)

		case protocol.MethodWorkspaceExecuteCommand:
			var params protocol.ExecuteCommandParams
			if err := decodeParams(req, &params); err != nil {
				return respond(nil, err)
			}
			if params.Command != ResolveDependencyOutputsCommand {
				return respond(nil, fmt.Errorf("unsupported command %q: %w", params.Command, jsonrpc2.ErrMethodNotFound))
			}
			args, err := decodeDependencyOutputArgs(params.Arguments)
			if err != nil {
				return respond(nil, fmt.Errorf("invalid command arguments: %w: %w", err, jsonrpc2.ErrInvalidParams))
			}
			result, err := s.executeDependencyOutputs(ctx, args)
			return respond(result, err)

		case protocol.MethodShutdown:
			s.shutdown.Store(true)
			return respond(nil, nil)

		case protocol.MethodExit:
			s.cleanupTempFiles()
			select {
			case <-s.exited:
			default:
				close(s.exited)
			}
			return respond(nil, nil)

		default:
			return jsonrpc2.MethodNotFoundHandler(ctx, reply, req)
		}
	}
}

func decodeParams(req jsonrpc2.Request, params any) error {
	if err := json.Unmarshal(req.Params(), params); err != nil {
		return fmt.Errorf("decode %s parameters: %w: %w", req.Method(), err, jsonrpc2.ErrInvalidParams)
	}

	return nil
}
