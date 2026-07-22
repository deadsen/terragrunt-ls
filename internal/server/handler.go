package server

import (
	"context"
	"encoding/json"
	"fmt"

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
			diagnostics := s.state.OpenDocument(ctx, s.log, params.TextDocument.URI, params.TextDocument.Text)
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
			diagnostics := s.state.UpdateDocument(ctx, s.log, params.TextDocument.URI, change.Text)
			return respond(nil, s.client.PublishDiagnostics(ctx, protocol.PublishDiagnosticsParams{
				URI:         params.TextDocument.URI,
				Diagnostics: diagnostics,
			}))

		case protocol.MethodTextDocumentDidSave:
			var params protocol.DidSaveTextDocumentParams
			if err := decodeParams(req, &params); err != nil {
				return respond(nil, err)
			}
			return respond(nil, nil)

		case protocol.MethodTextDocumentDidClose:
			var params protocol.DidCloseTextDocumentParams
			if err := decodeParams(req, &params); err != nil {
				return respond(nil, err)
			}
			return respond(nil, nil)

		case protocol.MethodTextDocumentHover:
			var params protocol.HoverParams
			if err := decodeParams(req, &params); err != nil {
				return respond(nil, err)
			}
			return respond(s.state.Hover(s.log, 0, params.TextDocument.URI, params.Position).Result, nil)

		case protocol.MethodTextDocumentDefinition:
			var params protocol.DefinitionParams
			if err := decodeParams(req, &params); err != nil {
				return respond(nil, err)
			}
			return respond(s.state.Definition(s.log, 0, params.TextDocument.URI, params.Position).Result, nil)

		case protocol.MethodTextDocumentCompletion:
			var params protocol.CompletionParams
			if err := decodeParams(req, &params); err != nil {
				return respond(nil, err)
			}
			return respond(s.state.TextDocumentCompletion(s.log, 0, params.TextDocument.URI, params.Position).Result, nil)

		case protocol.MethodTextDocumentFormatting:
			var params protocol.DocumentFormattingParams
			if err := decodeParams(req, &params); err != nil {
				return respond(nil, err)
			}
			return respond(s.state.TextDocumentFormatting(s.log, 0, params.TextDocument.URI).Result, nil)

		case protocol.MethodTextDocumentReferences:
			var params protocol.ReferenceParams
			if err := decodeParams(req, &params); err != nil {
				return respond(nil, err)
			}
			return respond(s.state.TextDocumentReferences(s.log, 0, params.TextDocument.URI, params.Position, params.Context.IncludeDeclaration).Result, nil)

		case protocol.MethodTextDocumentPrepareRename:
			var params protocol.PrepareRenameParams
			if err := decodeParams(req, &params); err != nil {
				return respond(nil, err)
			}
			return respond(s.state.PrepareRename(s.log, 0, params.TextDocument.URI, params.Position).Result, nil)

		case protocol.MethodTextDocumentRename:
			var params protocol.RenameParams
			if err := decodeParams(req, &params); err != nil {
				return respond(nil, err)
			}
			return respond(s.state.TextDocumentRename(s.log, 0, params.TextDocument.URI, params.Position, params.NewName).Result, nil)

		case protocol.MethodShutdown:
			s.shutdown.Store(true)
			return respond(nil, nil)

		case protocol.MethodExit:
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
