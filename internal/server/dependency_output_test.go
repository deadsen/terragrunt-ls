package server

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"terragrunt-ls/internal/ast"
	"terragrunt-ls/internal/testutils"
	"terragrunt-ls/internal/tg"
	"terragrunt-ls/internal/tg/dependency"
	"terragrunt-ls/internal/tg/store"

	"github.com/gruntwork-io/terragrunt/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zclconf/go-cty/cty"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

func TestDependencyOutputCodeActionsForBlockAndReference(t *testing.T) {
	t.Parallel()

	source := `dependency "app" {
  config_path = "../app"
}
inputs = { id = dependency.app.outputs.id }`
	indexed, err := ast.ParseHCLFile("/tmp/terragrunt.hcl", []byte(source))
	require.NoError(t, err)
	state := tg.NewState()
	docURI := protocol.DocumentURI("file:///tmp/terragrunt.hcl")
	state.Configs[docURI.Filename()] = store.Store{AST: indexed, Document: source, FileType: store.FileTypeUnit}
	server := New(testutils.NewTestLogger(t), state)

	for _, position := range []protocol.Position{
		{Line: 1, Character: 4},
		{Line: 3, Character: 35},
	} {
		actions := server.dependencyOutputCodeActions(protocol.CodeActionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: docURI},
			Range:        protocol.Range{Start: position, End: position},
		})
		require.Len(t, actions, 1)
		assert.Equal(t, protocol.RefactorRewrite, actions[0].Kind)
		require.NotNil(t, actions[0].Command)
		assert.Equal(t, ResolveDependencyOutputsCommand, actions[0].Command.Command)
		require.Len(t, actions[0].Command.Arguments, 1)
		assert.Equal(t, dependencyOutputArgs{URI: docURI, Dependency: "app"}, actions[0].Command.Arguments[0])
	}
}

func TestExecuteDependencyOutputsWritesSecureFileAndShowsDocument(t *testing.T) {
	server, docURI := dependencyOutputServer(t)
	shown := make(chan protocol.ShowDocumentParams, 1)
	bindTestClient(t, server, func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		if req.Method() == protocol.MethodShowDocument {
			var params protocol.ShowDocumentParams
			require.NoError(t, decodeParams(req, &params))
			shown <- params
			return reply(ctx, &protocol.ShowDocumentResult{Success: true}, nil)
		}
		return nil
	})

	result, err := server.executeDependencyOutputs(t.Context(), dependencyOutputArgs{URI: docURI, Dependency: "app"})
	require.NoError(t, err)
	path, ok := result.(string)
	require.True(t, ok)
	t.Cleanup(func() { _ = os.Remove(path) })
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":{"value":"123"}}`, string(contents))
	info, err := os.Stat(path)
	require.NoError(t, err)
	if os.PathSeparator == '/' {
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}

	select {
	case params := <-shown:
		assert.Equal(t, uri.File(path), params.URI)
		assert.True(t, params.TakeFocus)
	case <-time.After(time.Second):
		t.Fatal("window/showDocument was not requested")
	}
}

func TestExecuteDependencyOutputsFallsBackToShowMessage(t *testing.T) {
	server, docURI := dependencyOutputServer(t)
	messages := make(chan protocol.ShowMessageParams, 1)
	bindTestClient(t, server, func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		switch req.Method() {
		case protocol.MethodShowDocument:
			return jsonrpc2.MethodNotFoundHandler(ctx, reply, req)
		case protocol.MethodWindowShowMessage:
			var params protocol.ShowMessageParams
			require.NoError(t, decodeParams(req, &params))
			messages <- params
		}
		return nil
	})

	result, err := server.executeDependencyOutputs(t.Context(), dependencyOutputArgs{URI: docURI, Dependency: "app"})
	require.NoError(t, err)
	path := result.(string)
	t.Cleanup(func() { _ = os.Remove(path) })

	select {
	case message := <-messages:
		assert.Contains(t, message.Message, path)
	case <-time.After(time.Second):
		t.Fatal("window/showMessage fallback was not sent")
	}
}

func dependencyOutputServer(t *testing.T) (*Server, protocol.DocumentURI) {
	t.Helper()
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "unit")
	targetDir := filepath.Join(tmpDir, "app")
	require.NoError(t, os.MkdirAll(sourceDir, 0o755))
	require.NoError(t, os.MkdirAll(targetDir, 0o755))
	sourcePath := filepath.Join(sourceDir, "terragrunt.hcl")
	targetPath := filepath.Join(targetDir, "terragrunt.hcl")
	require.NoError(t, os.WriteFile(sourcePath, nil, 0o600))
	require.NoError(t, os.WriteFile(targetPath, nil, 0o600))

	state := tg.NewState()
	docURI := uri.File(sourcePath)
	state.Configs[sourcePath] = store.Store{Cfg: &config.TerragruntConfig{TerragruntDependencies: config.Dependencies{{
		Name: "app", ConfigPath: cty.StringVal("../app"),
	}}}}
	server := New(testutils.NewTestLogger(t), state)
	server.dependencyRunner = dependency.Runner{
		Timeout:  10 * time.Second,
		LookPath: func(string) (string, error) { return os.Args[0], nil },
		CommandContext: func(ctx context.Context, _ string, args ...string) *exec.Cmd {
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestDependencyOutputHelperProcess", "--")
			cmd.Args = append(cmd.Args, args...)
			cmd.Env = append(os.Environ(), "GO_WANT_DEPENDENCY_OUTPUT_HELPER=1")
			return cmd
		},
	}
	return server, docURI
}

func bindTestClient(t *testing.T, server *Server, handler jsonrpc2.Handler) {
	t.Helper()
	serverSide, clientSide := net.Pipe()
	t.Cleanup(func() {
		_ = serverSide.Close()
		_ = clientSide.Close()
	})
	serverConn := jsonrpc2.NewConn(jsonrpc2.NewStream(serverSide))
	clientConn := jsonrpc2.NewConn(jsonrpc2.NewStream(clientSide))
	serverConn.Go(t.Context(), jsonrpc2.MethodNotFoundHandler)
	clientConn.Go(t.Context(), handler)
	server.Bind(serverConn)
}

func TestDependencyOutputHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_DEPENDENCY_OUTPUT_HELPER") != "1" {
		return
	}
	arguments := strings.Join(os.Args, " ")
	if !strings.Contains(arguments, "output -json --config") {
		os.Exit(2)
	}
	encoded, _ := json.Marshal(map[string]any{"id": map[string]any{"value": "123"}})
	_, _ = os.Stdout.Write(encoded)
	os.Exit(0)
}

func TestInitializeAdvertisesDependencyOutputCommand(t *testing.T) {
	t.Parallel()

	server := New(testutils.NewTestLogger(t), tg.NewState())
	capabilities := server.initialize().Capabilities
	options, ok := capabilities.CodeActionProvider.(*protocol.CodeActionOptions)
	require.True(t, ok)
	assert.Equal(t, []protocol.CodeActionKind{protocol.RefactorRewrite}, options.CodeActionKinds)
	require.NotNil(t, capabilities.ExecuteCommandProvider)
	assert.Equal(t, []string{ResolveDependencyOutputsCommand}, capabilities.ExecuteCommandProvider.Commands)
}
