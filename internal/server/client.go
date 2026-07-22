package server

import (
	"context"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
)

type Client struct{ conn jsonrpc2.Conn }

func (c Client) PublishDiagnostics(ctx context.Context, params protocol.PublishDiagnosticsParams) error {
	return c.conn.Notify(ctx, protocol.MethodTextDocumentPublishDiagnostics, &params)
}

func (c Client) ShowMessage(ctx context.Context, params protocol.ShowMessageParams) error {
	return c.conn.Notify(ctx, protocol.MethodWindowShowMessage, &params)
}

func (c Client) ShowDocument(ctx context.Context, params protocol.ShowDocumentParams) (*protocol.ShowDocumentResult, error) {
	var result protocol.ShowDocumentResult
	_, err := c.conn.Call(ctx, protocol.MethodShowDocument, &params, &result)

	return &result, err
}
