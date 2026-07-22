package server

import (
	"context"
	"net"
	"terragrunt-ls/internal/testutils"
	"terragrunt-ls/internal/tg"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
)

func TestServeLifecycle(t *testing.T) {
	t.Parallel()
	serverSide, clientSide := net.Pipe()
	t.Cleanup(func() { _ = clientSide.Close() })

	s := New(testutils.NewTestLogger(t), tg.NewState())
	done := make(chan error, 1)
	go func() { done <- Serve(t.Context(), serverSide, s) }()

	conn := jsonrpc2.NewConn(jsonrpc2.NewStream(clientSide))
	conn.Go(t.Context(), jsonrpc2.MethodNotFoundHandler)

	var initialized struct {
		Capabilities struct {
			TextDocumentSync protocol.TextDocumentSyncOptions `json:"textDocumentSync"`
		} `json:"capabilities"`
	}
	_, err := conn.Call(t.Context(), protocol.MethodInitialize, protocol.InitializeParams{}, &initialized)
	require.NoError(t, err)
	assert.True(t, initialized.Capabilities.TextDocumentSync.OpenClose)
	assert.Equal(t, protocol.TextDocumentSyncKindFull, initialized.Capabilities.TextDocumentSync.Change)
	require.NotNil(t, initialized.Capabilities.TextDocumentSync.Save)

	require.NoError(t, conn.Notify(t.Context(), protocol.MethodInitialized, protocol.InitializedParams{}))
	_, err = conn.Call(t.Context(), protocol.MethodShutdown, nil, nil)
	require.NoError(t, err)
	_, err = conn.Call(t.Context(), protocol.MethodTextDocumentHover, protocol.HoverParams{}, nil)
	require.Error(t, err)
	require.NoError(t, conn.Notify(t.Context(), protocol.MethodExit, nil))
	require.NoError(t, <-done)
}

func TestServeDocumentLifecycle(t *testing.T) {
	t.Parallel()

	serverSide, clientSide := net.Pipe()
	t.Cleanup(func() { _ = clientSide.Close() })

	s := New(testutils.NewTestLogger(t), tg.NewState())
	done := make(chan error, 1)
	go func() { done <- Serve(t.Context(), serverSide, s) }()

	conn := jsonrpc2.NewConn(jsonrpc2.NewStream(clientSide))
	diagnostics := make(chan protocol.PublishDiagnosticsParams, 4)
	conn.Go(t.Context(), func(_ context.Context, _ jsonrpc2.Replier, req jsonrpc2.Request) error {
		if req.Method() != protocol.MethodTextDocumentPublishDiagnostics {
			return nil
		}

		var params protocol.PublishDiagnosticsParams
		if err := decodeParams(req, &params); err != nil {
			return err
		}
		diagnostics <- params

		return nil
	})

	uri := protocol.DocumentURI("file:///tmp/env.hcl")
	require.NoError(t, conn.Notify(t.Context(), protocol.MethodTextDocumentDidOpen, protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: uri, Version: 1, Text: "locals { a = 1 }"},
	}))
	require.Empty(t, receiveDiagnostics(t, diagnostics).Diagnostics)

	require.NoError(t, conn.Notify(t.Context(), protocol.MethodTextDocumentDidChange, protocol.DidChangeTextDocumentParams{
		TextDocument: protocol.VersionedTextDocumentIdentifier{
			TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: uri},
			Version:                2,
		},
		ContentChanges: []protocol.TextDocumentContentChangeEvent{{Text: "locals { a = 2 }"}},
	}))
	require.Empty(t, receiveDiagnostics(t, diagnostics).Diagnostics)

	require.NoError(t, conn.Notify(t.Context(), protocol.MethodTextDocumentDidSave, protocol.DidSaveTextDocumentParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
	}))
	require.Empty(t, receiveDiagnostics(t, diagnostics).Diagnostics)

	require.NoError(t, conn.Notify(t.Context(), protocol.MethodTextDocumentDidClose, protocol.DidCloseTextDocumentParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
	}))
	require.Empty(t, receiveDiagnostics(t, diagnostics).Diagnostics)
	_, ok := s.state.Document(uri)
	assert.False(t, ok)

	_, err := conn.Call(t.Context(), protocol.MethodShutdown, nil, nil)
	require.NoError(t, err)
	require.NoError(t, conn.Notify(t.Context(), protocol.MethodExit, nil))
	require.NoError(t, <-done)
}

func receiveDiagnostics(t *testing.T, diagnostics <-chan protocol.PublishDiagnosticsParams) protocol.PublishDiagnosticsParams {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	select {
	case params := <-diagnostics:
		return params
	case <-ctx.Done():
		require.NoError(t, ctx.Err())
		return protocol.PublishDiagnosticsParams{}
	}
}
