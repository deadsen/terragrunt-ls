package server

import (
	"net"
	"terragrunt-ls/internal/testutils"
	"terragrunt-ls/internal/tg"
	"testing"

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
