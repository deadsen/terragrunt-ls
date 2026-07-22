package server

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"

	"terragrunt-ls/internal/logger"
	"terragrunt-ls/internal/testutils"
	"terragrunt-ls/internal/tg"
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

func TestNewSharesStatePointer(t *testing.T) {
	t.Parallel()

	state := tg.NewState()
	s := New(testutils.NewTestLogger(t), state)

	assert.Same(t, state, s.state)
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

func TestStaleDidChangeDoesNotClearDiagnostics(t *testing.T) {
	t.Parallel()

	serverSide, clientSide := net.Pipe()
	t.Cleanup(func() { _ = clientSide.Close() })

	s := New(testutils.NewTestLogger(t), tg.NewState())
	done := make(chan error, 1)
	go func() { done <- Serve(t.Context(), serverSide, s) }()

	conn := jsonrpc2.NewConn(jsonrpc2.NewStream(clientSide))
	diagnostics := make(chan protocol.PublishDiagnosticsParams, 2)
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

	uri := protocol.DocumentURI("file:///tmp/stale-diagnostics.hcl")
	var result any
	_, err := conn.Call(t.Context(), protocol.MethodTextDocumentDidOpen, protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: uri, Version: 2, Text: "locals {"},
	}, &result)
	require.NoError(t, err)
	require.NotEmpty(t, receiveDiagnostics(t, diagnostics).Diagnostics)

	_, err = conn.Call(t.Context(), protocol.MethodTextDocumentDidChange, protocol.DidChangeTextDocumentParams{
		TextDocument: protocol.VersionedTextDocumentIdentifier{
			TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: uri},
			Version:                1,
		},
		ContentChanges: []protocol.TextDocumentContentChangeEvent{{Text: "locals { value = 1 }"}},
	}, &result)
	require.NoError(t, err)
	requireNoDiagnostics(t, diagnostics)

	_, err = conn.Call(t.Context(), protocol.MethodShutdown, nil, nil)
	require.NoError(t, err)
	require.NoError(t, conn.Notify(t.Context(), protocol.MethodExit, nil))
	require.NoError(t, <-done)
}

func TestStaleHoverReturnsHoverResultShape(t *testing.T) {
	t.Parallel()

	serverSide, clientSide := net.Pipe()
	t.Cleanup(func() { _ = clientSide.Close() })

	baseLog := testutils.NewTestLogger(t)
	blockingLog := newBlockingServerLogger(baseLog, "Hovering with context")
	s := New(blockingLog, tg.NewState())
	done := make(chan error, 1)
	go func() { done <- Serve(t.Context(), serverSide, s) }()

	conn := jsonrpc2.NewConn(jsonrpc2.NewStream(clientSide))
	diagnostics := make(chan protocol.PublishDiagnosticsParams, 1)
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

	uri := protocol.DocumentURI("file:///tmp/stale-hover.hcl")
	var notificationResult any
	_, err := conn.Call(t.Context(), protocol.MethodTextDocumentDidOpen, protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:     uri,
			Version: 1,
			Text:    "locals {\n  value = \"one\"\n  copy = local.value\n}",
		},
	}, &notificationResult)
	require.NoError(t, err)
	require.Empty(t, receiveDiagnostics(t, diagnostics).Diagnostics)

	hoverResult := make(chan json.RawMessage, 1)
	hoverErr := make(chan error, 1)
	go func() {
		var result json.RawMessage
		_, callErr := conn.Call(t.Context(), protocol.MethodTextDocumentHover, protocol.HoverParams{
			TextDocumentPositionParams: protocol.TextDocumentPositionParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: uri},
				Position:     protocol.Position{Line: 2, Character: 15},
			},
		}, &result)
		hoverResult <- result
		hoverErr <- callErr
	}()

	blockingLog.wait(t)
	s.state.UpdateDocument(t.Context(), baseLog, uri, "locals { value = \"two\" }", 2)
	blockingLog.unblock()
	require.NoError(t, <-hoverErr)
	got := <-hoverResult
	assert.Empty(t, got)

	_, err = conn.Call(t.Context(), protocol.MethodShutdown, nil, nil)
	require.NoError(t, err)
	require.NoError(t, conn.Notify(t.Context(), protocol.MethodExit, nil))
	require.NoError(t, <-done)
}

func TestFeatureResponseShapes(t *testing.T) {
	t.Parallel()

	serverSide, clientSide := net.Pipe()
	t.Cleanup(func() { _ = clientSide.Close() })
	server := New(testutils.NewTestLogger(t), tg.NewState())
	done := make(chan error, 1)
	go func() { done <- Serve(t.Context(), serverSide, server) }()

	conn := jsonrpc2.NewConn(jsonrpc2.NewStream(clientSide))
	diagnostics := make(chan protocol.PublishDiagnosticsParams, 1)
	conn.Go(t.Context(), func(_ context.Context, _ jsonrpc2.Replier, req jsonrpc2.Request) error {
		if req.Method() == protocol.MethodTextDocumentPublishDiagnostics {
			var params protocol.PublishDiagnosticsParams
			if err := decodeParams(req, &params); err != nil {
				return err
			}
			diagnostics <- params
		}
		return nil
	})

	docURI := protocol.DocumentURI("file:///tmp/terragrunt.hcl")
	document := "locals {\n  foo = \"bar\"\n}\ninputs = { value = local.foo }\n"
	require.NoError(t, conn.Notify(t.Context(), protocol.MethodTextDocumentDidOpen, protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: docURI, Version: 1, Text: document},
	}))
	_ = receiveDiagnostics(t, diagnostics)

	position := protocol.Position{Line: 3, Character: 27}
	calls := []struct {
		method string
		params any
	}{
		{protocol.MethodTextDocumentHover, protocol.HoverParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: docURI}, Position: position}}},
		{protocol.MethodTextDocumentDefinition, protocol.DefinitionParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: docURI}, Position: position}}},
		{protocol.MethodTextDocumentReferences, protocol.ReferenceParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: docURI}, Position: position}, Context: protocol.ReferenceContext{IncludeDeclaration: true}}},
		{protocol.MethodTextDocumentPrepareRename, protocol.PrepareRenameParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: docURI}, Position: position}}},
		{protocol.MethodTextDocumentRename, protocol.RenameParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: docURI}, Position: position}, NewName: "renamed"}},
		{protocol.MethodTextDocumentCompletion, protocol.CompletionParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: docURI}, Position: protocol.Position{Line: 4, Character: 0}}}},
		{protocol.MethodTextDocumentFormatting, protocol.DocumentFormattingParams{TextDocument: protocol.TextDocumentIdentifier{URI: docURI}}},
	}
	for _, call := range calls {
		var raw json.RawMessage
		_, err := conn.Call(t.Context(), call.method, call.params, &raw)
		require.NoError(t, err, call.method)
		assertNoProtocolEnvelope(t, raw)
	}

	_, err := conn.Call(t.Context(), protocol.MethodShutdown, nil, nil)
	require.NoError(t, err)
	require.NoError(t, conn.Notify(t.Context(), protocol.MethodExit, nil))
	require.NoError(t, <-done)
}

func assertNoProtocolEnvelope(t *testing.T, raw json.RawMessage) {
	t.Helper()
	if len(raw) == 0 {
		return
	}
	var value any
	require.NoError(t, json.Unmarshal(raw, &value))
	var inspect func(any)
	inspect = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			for _, key := range []string{"jsonrpc", "id", "result"} {
				assert.NotContains(t, typed, key, string(raw))
			}
			for _, nested := range typed {
				inspect(nested)
			}
		case []any:
			for _, nested := range typed {
				inspect(nested)
			}
		}
	}
	inspect(value)
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

func requireNoDiagnostics(t *testing.T, diagnostics <-chan protocol.PublishDiagnosticsParams) {
	t.Helper()

	select {
	case params := <-diagnostics:
		t.Fatalf("unexpected diagnostics notification: %#v", params)
	case <-time.After(250 * time.Millisecond):
	}
}

type blockingServerLogger struct {
	logger.Logger
	message string
	reached chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingServerLogger(l logger.Logger, message string) *blockingServerLogger {
	return &blockingServerLogger{
		Logger:  l,
		message: message,
		reached: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (l *blockingServerLogger) Debug(message string, args ...any) {
	if message == l.message {
		l.once.Do(func() { close(l.reached) })
		<-l.release
	}
	l.Logger.Debug(message, args...)
}

func (l *blockingServerLogger) wait(t *testing.T) {
	t.Helper()

	select {
	case <-l.reached:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for hover result")
	}
}

func (l *blockingServerLogger) unblock() {
	close(l.release)
}
