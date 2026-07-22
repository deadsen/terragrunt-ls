package server

import (
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"terragrunt-ls/internal/logger"
	"terragrunt-ls/internal/tg"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
)

type Server struct {
	log      logger.Logger
	state    tg.State
	client   Client
	conn     jsonrpc2.Conn
	shutdown atomic.Bool
	exited   chan struct{}
}

func New(log logger.Logger, state tg.State) *Server {
	return &Server{log: log, state: state, exited: make(chan struct{})}
}

func (s *Server) Bind(conn jsonrpc2.Conn) {
	s.conn = conn
	s.client = Client{conn: conn}
}

func (s *Server) initialize() protocol.InitializeResult {
	return protocol.InitializeResult{
		Capabilities: protocol.ServerCapabilities{
			TextDocumentSync: &protocol.TextDocumentSyncOptions{
				OpenClose: true,
				Change:    protocol.TextDocumentSyncKindFull,
				Save:      &protocol.SaveOptions{IncludeText: false},
			},
			HoverProvider:              true,
			DefinitionProvider:         true,
			ReferencesProvider:         true,
			CompletionProvider:         &protocol.CompletionOptions{},
			DocumentFormattingProvider: true,
			RenameProvider:             &protocol.RenameOptions{PrepareProvider: true},
		},
		ServerInfo: &protocol.ServerInfo{Name: "terragrunt-ls", Version: "0.0.1"},
	}
}

func Serve(ctx context.Context, rwc io.ReadWriteCloser, s *Server) error {
	conn := jsonrpc2.NewConn(jsonrpc2.NewStream(rwc))
	s.Bind(conn)
	conn.Go(ctx, protocol.Handlers(s.Handler()))

	select {
	case <-s.exited:
		_ = conn.Close()
	case <-conn.Done():
	}
	<-conn.Done()
	if err := conn.Err(); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) && !errors.Is(err, net.ErrClosed) {
		return err
	}

	return nil
}
