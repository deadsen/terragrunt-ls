package main

import (
	"context"
	"os"
	"terragrunt-ls/internal/config"
	"terragrunt-ls/internal/logger"
	"terragrunt-ls/internal/rpc"
	"terragrunt-ls/internal/server"
	"terragrunt-ls/internal/tg"
)

func main() {
	cfg := config.Load()

	l := logger.NewLogger(cfg.LogFile, cfg.LogLevel)
	defer func() {
		if err := l.Close(); err != nil {
			panic(err)
		}
	}()

	l.Info("Initializing terragrunt-ls")

	ctx := context.Background()
	s := server.New(l, tg.NewState())
	if err := server.Serve(ctx, rpc.NewStdio(os.Stdin, os.Stdout), s); err != nil {
		l.Error("Language server stopped", "error", err)
	}
}
