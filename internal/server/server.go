// Package server implements the Terragrunt language server protocol handlers.
package server

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"terragrunt-ls/internal/logger"
	"terragrunt-ls/internal/tg"
	"terragrunt-ls/internal/tg/dependency"
	"time"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
)

const defaultDependencyTimeout = time.Minute

type Server struct {
	log              logger.Logger
	client           Client
	conn             jsonrpc2.Conn
	state            *tg.State
	exited           chan struct{}
	tempFiles        map[string]struct{}
	dependencyRunner dependency.Runner
	tempMu           sync.Mutex
	shutdown         atomic.Bool
}

func New(log logger.Logger, state *tg.State) *Server {
	return &Server{
		log:              log,
		state:            state,
		exited:           make(chan struct{}),
		dependencyRunner: dependency.NewRunner(defaultDependencyTimeout),
		tempFiles:        make(map[string]struct{}),
	}
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
			CodeActionProvider: &protocol.CodeActionOptions{
				CodeActionKinds: []protocol.CodeActionKind{protocol.RefactorRewrite},
			},
			ExecuteCommandProvider: &protocol.ExecuteCommandOptions{
				Commands: []string{ResolveDependencyOutputsCommand},
			},
		},
		ServerInfo: &protocol.ServerInfo{Name: "terragrunt-ls", Version: "0.1.0"},
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
