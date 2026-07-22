# Terragrunt Language Server and Editor Parity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add every identified Terragrunt language feature to the official server foundation and expose identical behavior through the Zed and VS Code extensions.

**Architecture:** Keep the official Terragrunt parsing, stack/value support, and feature packages. Replace only the manual synchronous transport with a bounded `go.lsp.dev/jsonrpc2` router, then add shared symbol, path, diagnostics, and dependency-output services consumed by thin LSP handlers. Zed and VS Code launch the same native binary and own only native filename association, syntax highlighting, and packaging.

**Tech Stack:** Go 1.26.2, `go.lsp.dev/protocol` v0.12.0, `go.lsp.dev/jsonrpc2` v0.10.0, HashiCorp HCL v2, Terragrunt v1.0.2, Rust 1.85.1 with `zed_extension_api` 0.2.0, Node 23.10.0, TypeScript 5.8.

## Global Constraints

- Preserve existing unit, stack, values, formatting, completion, and Zed outline behavior.
- The server must use existing `go.lsp.dev/protocol` types; do not copy the reference repository's generated protocol tree.
- Default editor filenames are exactly `terragrunt.hcl`, `root.hcl`, `terragrunt.stack.hcl`, and `terragrunt.values.hcl`.
- Extra filenames use VS Code `files.associations` or Zed `file_types`; do not add a Terragrunt-specific filename setting.
- A document opened with language ID `terragrunt` is a unit file unless its basename is `terragrunt.stack.hcl` or `terragrunt.values.hcl`.
- Dependency output resolution runs only from an explicit code action, invokes `terragrunt` without a shell, and has a 60-second timeout.
- This phase must not publish releases or download/update binaries automatically.
- Write tests before implementation and run the narrow test before the full package suite.
- Do not create commits unless the user explicitly authorizes commits during execution. The commit steps below are checkpoints to use only after that authorization; otherwise skip them.

---

## File Structure

### New server and transport files

- `internal/server/server.go`: lifecycle state, advertised capabilities, and shared dependencies.
- `internal/server/handler.go`: JSON-RPC method routing and protocol parameter decoding.
- `internal/server/client.go`: diagnostics, messages, and `window/showDocument` calls to the editor.
- `internal/server/server_test.go`: framed-protocol lifecycle and bidirectional request tests.
- `internal/rpc/stdio.go`: `io.ReadWriteCloser` adapter for LSP stdio.

### New semantic service files

- `internal/tg/symbol/symbol.go`: local/include/dependency target and occurrence model.
- `internal/tg/symbol/symbol_test.go`: declaration/reference/range tests.
- `internal/tg/path/path.go`: include, dependency, dependency-target, and `file(...)` resolution.
- `internal/tg/path/path_test.go`: literal/evaluated/missing-path tests.
- `internal/tg/diagnostics/diagnostics.go`: semantic diagnostic construction and false-positive filtering.
- `internal/tg/diagnostics/diagnostics_test.go`: missing symbol/path and duplicate-local tests.
- `internal/tg/dependency/output.go`: safe Terragrunt subprocess execution and JSON formatting.
- `internal/tg/dependency/output_test.go`: fake-binary, timeout, cancellation, and error tests.

### Existing files with focused changes

- `main.go`: construct and run the new server.
- `go.mod`, `go.sum`: make `go.lsp.dev/jsonrpc2` a direct dependency.
- `internal/ast/ast.go`, `internal/ast/position.go`, tests: dependency indexing and UTF-16 positions.
- `internal/tg/store/store.go`: document version and semantic diagnostics.
- `internal/tg/state.go`, tests: versioned lifecycle and protocol-neutral feature results.
- `internal/tg/hover/*`, `completion/*`, `definition/*`, `rename/*`, `references/*`: richer shared behavior.
- `internal/tg/parse.go`, tests: merge semantic diagnostics with parser diagnostics.
- `internal/lsp/initialize.go`: remove after capabilities move into `internal/server`.
- Remaining `internal/lsp/*.go`: remove after state methods return protocol result types directly.
- `internal/rpc/rpc.go`, `internal/rpc/rpc_test.go`: replace the hand-written framer with `stdio.go`.
- `zed-extension/src/lib.rs`: native `binary.path` followed by `PATH` lookup.
- `zed-extension/languages/terragrunt/config.toml`: canonical filename suffixes.
- `zed-extension/languages/terragrunt/highlights.scm`: Terragrunt traversal scopes.
- `vscode-extension/package.json`: canonical filenames, activation, and tests.
- `vscode-extension/src/extension.ts`: retain thin client behavior and narrow watched files.
- `vscode-extension/syntaxes/terragrunt.tmGrammar.json`: traversal parity.
- `docs/server-capabilities.md`, `docs/setup.md`, editor READMEs: final behavior and local setup.

---

### Task 1: Bidirectional JSON-RPC Transport and LSP Lifecycle

**Files:**
- Create: `internal/rpc/stdio.go`
- Create: `internal/server/client.go`
- Create: `internal/server/server.go`
- Create: `internal/server/handler.go`
- Create: `internal/server/server_test.go`
- Modify: `main.go`
- Modify: `go.mod`
- Delete: `internal/rpc/rpc.go`
- Delete: `internal/rpc/rpc_test.go`

**Interfaces:**
- Produces: `rpc.NewStdio(io.Reader, io.Writer) io.ReadWriteCloser`.
- Produces: `server.New(logger.Logger, tg.State) *server.Server`.
- Produces: `(*server.Server).Bind(jsonrpc2.Conn)` and `(*server.Server).Handler() jsonrpc2.Handler`.
- Produces: `server.Serve(context.Context, io.ReadWriteCloser, *Server) error`.
- Preserves all currently advertised feature results by unwrapping existing `internal/lsp` response structs.

- [ ] **Step 1: Write a failing framed lifecycle test**

Add a `net.Pipe` test that starts `Serve`, calls `initialize`, checks full-sync/open-close/save capabilities, sends `initialized`, calls `shutdown`, sends `exit`, and verifies clean termination:

```go
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
	require.NoError(t, conn.Notify(t.Context(), protocol.MethodExit, nil))
	require.NoError(t, <-done)
}
```

- [ ] **Step 2: Run the lifecycle test and verify it fails**

Run: `go test ./internal/server -run TestServeLifecycle -count=1`

Expected: FAIL because `internal/server` and `Serve` do not exist.

- [ ] **Step 3: Add the stdio adapter and server/client types**

Implement the adapter without closing process stdio:

```go
package rpc

import "io"

type stdio struct {
	io.Reader
	io.Writer
}

func NewStdio(r io.Reader, w io.Writer) io.ReadWriteCloser {
	return &stdio{Reader: r, Writer: w}
}

func (*stdio) Close() error { return nil }
```

Define a client that uses notifications for diagnostics/messages and a correlated call for `window/showDocument`:

```go
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
```

Define lifecycle state and capabilities:

```go
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
				Change: protocol.TextDocumentSyncKindFull,
				Save: &protocol.SaveOptions{IncludeText: false},
			},
			HoverProvider: true,
			DefinitionProvider: true,
			ReferencesProvider: true,
			CompletionProvider: &protocol.CompletionOptions{},
			DocumentFormattingProvider: true,
			RenameProvider: &protocol.RenameOptions{PrepareProvider: true},
		},
		ServerInfo: &protocol.ServerInfo{Name: "terragrunt-ls", Version: "0.0.1"},
	}
}
```

- [ ] **Step 4: Implement routing and serving**

Use `encoding/json` to decode `req.Params()` and one helper that always replies. Parameter decode failures must wrap `jsonrpc2.ErrInvalidParams`; unknown methods delegate to `jsonrpc2.MethodNotFoundHandler`. Route the existing methods plus initialized, didSave, didClose, shutdown, and exit. Because the server advertises full sync, `didChange` uses the final content change's complete `Text` value. The lifecycle cases are:

```go
case protocol.MethodInitialize:
	return reply(ctx, s.initialize(), nil)
case protocol.MethodInitialized:
	return reply(ctx, nil, nil)
case protocol.MethodShutdown:
	s.shutdown.Store(true)
	return reply(ctx, nil, nil)
case protocol.MethodExit:
	select {
	case <-s.exited:
	default:
		close(s.exited)
	}
	return reply(ctx, nil, nil)
```

Implement `Serve` with the library's cancellation, asynchronous stream reader, response correlation, and method-not-found handling:

```go
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
	if err := conn.Err(); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}
```

At the start of routing, if `s.shutdown.Load()` is true and the method is not `exit`, reply with an error wrapping `jsonrpc2.ErrInvalidRequest`. Add a lifecycle assertion that a hover request after shutdown is rejected.

Replace `main`'s scanner loop with:

```go
ctx := context.Background()
s := server.New(l, tg.NewState())
if err := server.Serve(ctx, rpc.NewStdio(os.Stdin, os.Stdout), s); err != nil {
	l.Error("Language server stopped", "error", err)
}
```

Move `go.lsp.dev/jsonrpc2 v0.10.0` from indirect to the direct `require` block. Remove the obsolete hand-written framing files only after the lifecycle test compiles.

- [ ] **Step 5: Run transport tests and the existing suite**

Run: `go test ./internal/server ./internal/rpc -count=1`

Expected: PASS.

Run: `go test ./... -count=1`

Expected: PASS with all existing hover, definition, references, rename, completion, formatting, stack, and values tests unchanged in behavior.

- [ ] **Step 6: Commit the transport checkpoint if commits are authorized**

```bash
git add go.mod go.sum main.go internal/rpc internal/server
git commit -m "refactor: add bidirectional LSP transport"
```

### Task 2: Versioned Document Lifecycle and Native Extra Filenames

**Files:**
- Modify: `internal/tg/store/store.go`
- Modify: `internal/tg/state.go`
- Modify: `internal/tg/state_test.go`
- Modify: `internal/tg/parse.go`
- Modify: `internal/server/handler.go`
- Modify: `internal/server/server_test.go`

**Interfaces:**
- Produces: `State.OpenDocument(ctx, log, uri, text, version) []protocol.Diagnostic`.
- Produces: `State.UpdateDocument(ctx, log, uri, text, version) []protocol.Diagnostic`.
- Produces: `State.SaveDocument(ctx, log, uri) []protocol.Diagnostic`.
- Produces: `State.CloseDocument(uri)`.
- Produces: `State.Document(uri) (store.Store, bool)`.
- Produces: `State.IsCurrent(uri, version) bool` for discarding stale feature results.

- [ ] **Step 1: Write failing lifecycle and filename tests**

Add tests that assert `root.hcl` and a user-associated `env.hcl` are unit files, stale versions are ignored, save re-runs analysis, and close removes state:

```go
func TestStateVersionedLifecycle(t *testing.T) {
	t.Parallel()
	l := testutils.NewTestLogger(t)
	s := tg.NewState()
	uri := protocol.DocumentURI("file:///tmp/env.hcl")

	require.Empty(t, s.OpenDocument(t.Context(), l, uri, "locals { a = 1 }", 3))
	st, ok := s.Document(uri)
	require.True(t, ok)
	assert.Equal(t, store.FileTypeUnit, st.FileType)
	assert.Equal(t, int32(3), st.Version)

	s.UpdateDocument(t.Context(), l, uri, "locals { a = 2 }", 2)
	st, _ = s.Document(uri)
	assert.Equal(t, "locals { a = 1 }", st.Document)

	require.Empty(t, s.SaveDocument(t.Context(), l, uri))
	s.CloseDocument(uri)
	_, ok = s.Document(uri)
	assert.False(t, ok)
}

func TestDetectFileType(t *testing.T) {
	assert.Equal(t, store.FileTypeUnit, tg.DetectFileType("/x/terragrunt.hcl"))
	assert.Equal(t, store.FileTypeUnit, tg.DetectFileType("/x/root.hcl"))
	assert.Equal(t, store.FileTypeUnit, tg.DetectFileType("/x/env.hcl"))
	assert.Equal(t, store.FileTypeStack, tg.DetectFileType("/x/terragrunt.stack.hcl"))
	assert.Equal(t, store.FileTypeValues, tg.DetectFileType("/x/terragrunt.values.hcl"))
}
```

- [ ] **Step 2: Run the lifecycle tests and verify they fail**

Run: `go test ./internal/tg -run 'TestStateVersionedLifecycle|TestDetectFileType' -count=1`

Expected: FAIL because `Version`, `Document`, `SaveDocument`, and `CloseDocument` do not exist and method signatures lack versions.

- [ ] **Step 3: Add version-aware state operations**

Add `Version int32` to `store.Store`. Store `State.Configs` behind `sync.RWMutex`, return snapshots through `Document`, and reject only an update whose version is lower than the stored version:

```go
type State struct {
	mu      sync.RWMutex
	Configs map[string]store.Store
}

func (s *State) Document(docURI protocol.DocumentURI) (store.Store, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.Configs[docURI.Filename()]
	return st, ok
}

func (s *State) CloseDocument(docURI protocol.DocumentURI) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.Configs, docURI.Filename())
}

func (s *State) IsCurrent(docURI protocol.DocumentURI, version int32) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.Configs[docURI.Filename()]
	return ok && st.Version == version
}
```

Pass the version into `updateState`. Check the stored version under a read lock before parsing, then check it again under a write lock before replacing the snapshot. `SaveDocument` retrieves the current text/version and calls `updateState` with the same version.

Change filename detection to:

```go
func DetectFileType(filename string) store.FileType {
	switch filepath.Base(filename) {
	case "terragrunt.stack.hcl":
		return store.FileTypeStack
	case "terragrunt.values.hcl":
		return store.FileTypeValues
	default:
		return store.FileTypeUnit
	}
}
```

Update all existing state tests and handlers to pass document versions. Each interactive handler captures the snapshot version and calls `IsCurrent` before replying; if the version changed while it worked, reply with the feature's empty result instead. `didClose` must clear diagnostics before deleting state:

```go
s.state.CloseDocument(params.TextDocument.URI)
err := s.client.PublishDiagnostics(ctx, protocol.PublishDiagnosticsParams{
	URI: params.TextDocument.URI,
	Diagnostics: []protocol.Diagnostic{},
})
return reply(ctx, nil, err)
```

- [ ] **Step 4: Run lifecycle, race, and full tests**

Run: `go test -race ./internal/tg ./internal/server -count=1`

Expected: PASS with no data races.

Run: `go test ./... -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the lifecycle checkpoint if commits are authorized**

```bash
git add internal/tg internal/server
git commit -m "feat: track complete document lifecycle"
```

### Task 3: Shared Symbol Model and UTF-16 Position Conversion

**Files:**
- Modify: `internal/ast/ast.go`
- Modify: `internal/ast/ast_test.go`
- Modify: `internal/ast/position.go`
- Create: `internal/ast/position_test.go`
- Create: `internal/tg/symbol/symbol.go`
- Create: `internal/tg/symbol/symbol_test.go`
- Modify: `internal/tg/rename/rename.go`
- Modify: `internal/tg/references/references.go`
- Modify: affected tests under `internal/tg`

**Interfaces:**
- Produces: `IndexedAST.Dependencies ast.Scope`.
- Produces: `ast.FromHCLRange(source string, hcl.Range) protocol.Range` and `ast.ToHCLPos(source string, protocol.Position) hcl.Pos`.
- Produces: `symbol.Kind`, `symbol.Target`, `symbol.At`, and `symbol.Occurrences`.

- [ ] **Step 1: Write failing dependency-symbol and UTF-16 tests**

```go
func TestUTF16PositionRoundTrip(t *testing.T) {
	source := "😀a\n"
	hclPos := hcl.Pos{Line: 1, Column: 6, Byte: 5}
	lspPos := ast.FromHCLPos(source, hclPos)
	assert.Equal(t, uint32(3), lspPos.Character)
	assert.Equal(t, hclPos, ast.ToHCLPos(source, lspPos))
}

func TestDependencyAndIncludeOccurrences(t *testing.T) {
	source := `dependency "app" { config_path = "../app" }
include "root" { path = find_in_parent_folders("root.hcl") }
inputs = {
  id = dependency.app.outputs.id
  x  = include.root.inputs.x
}`
	iast, err := ast.ParseHCLFile("terragrunt.hcl", []byte(source))
	require.NoError(t, err)

	target, ok := symbol.At(iast, source, protocol.Position{Line: 3, Character: 18})
	require.True(t, ok)
	assert.Equal(t, symbol.Dependency, target.Kind)
	assert.Equal(t, "app", target.Name)
	assert.Len(t, symbol.Occurrences(iast, source, target, true), 2)
}
```

- [ ] **Step 2: Run the focused tests and verify they fail**

Run: `go test ./internal/ast ./internal/tg/symbol -count=1`

Expected: FAIL because dependency indexing, source-aware conversion, and the symbol package are missing.

- [ ] **Step 3: Index dependency declarations and implement UTF-16 conversion**

Add a `dependencies Scope` field to `nodeIndexBuilder`, populate it when `IsDependencyBlock(inode)` is true, and expose it as `IndexedAST.Dependencies`.

Implement source-aware conversion by splitting the requested line and using `unicode/utf16`:

```go
func utf16Column(line []byte, byteColumn int) uint32 {
	byteColumn = min(max(byteColumn, 0), len(line))
	runes := []rune(string(line[:byteColumn]))
	return uint32(len(utf16.Encode(runes)))
}

func byteColumn(line []byte, units uint32) int {
	used := uint32(0)
	for offset, r := range string(line) {
		width := uint32(1)
		if r > 0xFFFF {
			width = 2
		}
		if used+width > units {
			return offset
		}
		used += width
	}
	return len(line)
}
```

`FromHCLPos` converts HCL's 1-based byte column into UTF-16 units. `ToHCLPos` converts UTF-16 units back to a 1-based byte column and computes the absolute byte offset. Update every existing caller to provide `store.Document`.

- [ ] **Step 4: Implement the shared symbol model**

Use these exact public types:

```go
type Kind string

const (
	Local Kind = "local"
	Dependency Kind = "dependency"
	Include Kind = "include"
)

type Target struct {
	Kind Kind
	Name string
	Range hcl.Range
	Declaration bool
}

type Occurrence struct {
	Range protocol.Range
	Declaration bool
}
```

`At` must recognize local attribute name ranges, dependency/include block label ranges, and the second traversal step of `local.<name>`, `dependency.<name>`, and `include.<name>`. `Occurrences` walks the AST, emits the matching declaration plus matching traversal second steps, removes duplicate ranges, sorts by line/character, and respects `includeDeclaration`.

Make `rename.GetRenameTarget` and `references.GetReferences` delegate to this package so later tasks share one resolution model.

- [ ] **Step 5: Run symbol, position, and regression tests**

Run: `go test ./internal/ast ./internal/tg/symbol ./internal/tg/rename ./internal/tg/references -count=1`

Expected: PASS.

Run: `go test ./... -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the symbol checkpoint if commits are authorized**

```bash
git add internal/ast internal/tg/symbol internal/tg/rename internal/tg/references internal/tg/state.go
git commit -m "feat: add shared Terragrunt symbol model"
```

### Task 4: Nested Local Hover and Prefix-Only Completion Edits

**Files:**
- Modify: `internal/tg/hover/hover.go`
- Modify: `internal/tg/hover/hover_test.go`
- Modify: `internal/tg/completion/completion.go`
- Modify: `internal/tg/completion/completion_test.go`
- Modify: `internal/tg/state.go`
- Modify: `internal/tg/state_test.go`

**Interfaces:**
- Produces: `hover.GetLocalPath(store.Store, protocol.Position) ([]string, protocol.Range, bool)`.
- Produces: `completion.PrefixRange(document string, position protocol.Position) (string, protocol.Range)`.

- [ ] **Step 1: Write failing nested-hover and indented-prefix tests**

```go
func TestNestedLocalHover(t *testing.T) {
	source := `locals {
  service = { database = { port = 5432 } }
}
inputs = { port = local.service.database.port }`
	l := testutils.NewTestLogger(t)
	s := tg.NewState()
	uri := protocol.DocumentURI("file:///tmp/terragrunt.hcl")
	s.OpenDocument(t.Context(), l, uri, source, 1)

	hover := s.Hover(l, 1, uri, protocol.Position{Line: 3, Character: 39})
	assert.Contains(t, hover.Result.Contents.Value, "port = 5432")
}

func TestCompletionReplacesOnlyTypedPrefix(t *testing.T) {
	st := store.Store{Document: "  dep", FileType: store.FileTypeUnit}
	items := completion.GetCompletions(testutils.NewTestLogger(t), st, protocol.Position{Line: 0, Character: 5})
	require.NotEmpty(t, items)
	assert.Equal(t, protocol.Position{Line: 0, Character: 2}, items[0].TextEdit.Range.Start)
	assert.Equal(t, protocol.Position{Line: 0, Character: 5}, items[0].TextEdit.Range.End)
}
```

- [ ] **Step 2: Run the focused tests and verify they fail**

Run: `go test ./internal/tg/hover ./internal/tg/completion ./internal/tg -run 'Nested|Prefix' -count=1`

Expected: FAIL because hover accepts only two traversal parts and completions start at column zero.

- [ ] **Step 3: Implement nested local selection and value traversal**

Replace word splitting with AST traversal. Build the attribute path from each `hcl.TraverseAttr` after the `local` root until the attribute containing the cursor. Resolve it from `st.CfgAsCty.GetAttr("locals")` using attributes first and string map indices second:

```go
func localValue(value cty.Value, path []string) (cty.Value, bool) {
	for _, name := range path {
		if value.IsMarked() || !value.IsKnown() || value.IsNull() {
			return cty.NilVal, false
		}
		switch {
		case value.Type().HasAttribute(name):
			value = value.GetAttr(name)
		case value.Type().IsMapType() && value.HasIndex(cty.StringVal(name)).True():
			value = value.Index(cty.StringVal(name))
		default:
			return cty.NilVal, false
		}
	}
	return value, !value.IsMarked() && value.IsKnown() && !value.IsNull()
}
```

Render the final selected attribute name and value as the existing fenced HCL hover. Add a marked-value unit test and return no hover for marked values so potentially sensitive data is never revealed.

- [ ] **Step 4: Implement prefix-only completion edits**

Scan backward on the cursor line while characters are letters, digits, `_`, or `-`. Filter candidates using that prefix and assign every candidate's edit range to the prefix range. Preserve all existing unit and stack snippets unchanged.

```go
func PrefixRange(document string, position protocol.Position) (string, protocol.Range) {
	line := text.Line(document, position.Line)
	end := min(int(position.Character), len(line))
	start := end
	for start > 0 {
		r := rune(line[start-1])
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' {
			break
		}
		start--
	}
	return line[start:end], protocol.Range{
		Start: protocol.Position{Line: position.Line, Character: uint32(start)},
		End: position,
	}
}
```

- [ ] **Step 5: Run hover/completion and full tests**

Run: `go test ./internal/tg/hover ./internal/tg/completion ./internal/tg -count=1`

Expected: PASS.

Run: `go test ./... -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the interaction checkpoint if commits are authorized**

```bash
git add internal/tg/hover internal/tg/completion internal/tg/state.go internal/tg/state_test.go
git commit -m "feat: improve hover and completion ranges"
```

### Task 5: Rich Terragrunt Path and Definition Resolution

**Files:**
- Create: `internal/tg/path/path.go`
- Create: `internal/tg/path/path_test.go`
- Modify: `internal/ast/ast.go`
- Modify: `internal/tg/definition/definition.go`
- Modify: `internal/tg/definition/definition_test.go`
- Modify: `internal/tg/state.go`
- Modify: `internal/tg/state_definition_test.go`

**Interfaces:**
- Produces: `path.DependencyConfig(store.Store, string) (string, error)`.
- Produces: `path.DependencyTarget(sourceFile, configPath string) (string, error)`.
- Produces: `path.Include(store.Store, string) (string, error)`.
- Produces: `path.FileCall(store.Store, *ast.IndexedNode) (string, error)`.
- Produces: `definition.Resolve(store.Store, protocol.DocumentURI, protocol.Position) []protocol.Location`.

- [ ] **Step 1: Write failing path and definition tests**

Create temporary `app/terragrunt.hcl`, `root.hcl`, and `data.json` files. Test navigation from:

```hcl
locals {
  dep_dir = "../app"
  data    = file("data.json")
}

include "root" {
  path = find_in_parent_folders("root.hcl")
}

dependency "app" {
  config_path = local.dep_dir
}

inputs = {
  id = dependency.app.outputs.id
}
```

Assert that positions on `local.dep_dir`, `include.root`, the `config_path` expression, `dependency.app.outputs.id`, and the argument to `file(...)` return the declaration or target file URI. Also assert that a missing target returns no location and a typed error from the path service.

- [ ] **Step 2: Run definition tests and verify they fail**

Run: `go test ./internal/tg/path ./internal/tg/definition ./internal/tg -run Definition -count=1`

Expected: FAIL because path services and rich definition contexts do not exist.

- [ ] **Step 3: Implement path resolution**

Use already-evaluated official parser data first. `DependencyConfig` finds the named `st.Cfg.TerragruntDependencies` entry and accepts only known, non-null `cty.String` values. `Include` reads `st.Cfg.ProcessedIncludes`. When parser evaluation is unavailable, evaluate a literal string expression directly; do not guess unknown values.

Resolve a dependency target by cleaning a path relative to the source file. Accept an existing regular file directly; otherwise try `terragrunt.hcl` and `terragrunt.hcl.json` inside the directory. Return errors that include dependency name/path.

`FileCall` walks parents to a `*hclsyntax.FunctionCallExpr` named `file`, evaluates its first argument to a known string using the document's local evaluation context, resolves it relative to the source file, and requires an existing regular file.

- [ ] **Step 4: Replace definition context branching with one resolver**

`definition.Resolve` must follow this order:

1. Resolve a shared symbol at the cursor.
2. For `local`, return its declaration range in the current URI.
3. For `dependency`, resolve its config path and target file.
4. For `include`, return its processed include path.
5. If the cursor is inside a dependency `config_path`, resolve that dependency target.
6. If the cursor is inside an include `path`, resolve the include target.
7. If the cursor is inside `file(...)`, resolve the referenced regular file.

Return `nil` when no definition exists; do not return the old synthetic location at the cursor. Change the state method to return `[]protocol.Location` so the handler replies with an LSP-compliant definition array.

- [ ] **Step 5: Run path, definition, and full tests**

Run: `go test ./internal/tg/path ./internal/tg/definition ./internal/tg -run Definition -count=1`

Expected: PASS.

Run: `go test ./... -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the navigation checkpoint if commits are authorized**

```bash
git add internal/ast internal/tg/path internal/tg/definition internal/tg/state.go internal/tg/state_definition_test.go
git commit -m "feat: add Terragrunt-aware definition navigation"
```

### Task 6: Dependency and Include References and Rename

**Files:**
- Modify: `internal/tg/symbol/symbol.go`
- Modify: `internal/tg/symbol/symbol_test.go`
- Modify: `internal/tg/rename/rename.go`
- Modify: `internal/tg/rename/rename_test.go`
- Modify: `internal/tg/references/references.go`
- Modify: `internal/tg/references/references_test.go`
- Modify: `internal/tg/state.go`
- Modify: `internal/tg/state_rename_test.go`

**Interfaces:**
- Consumes: `symbol.At` and `symbol.Occurrences` from Task 3.
- Produces: rename edits for local identifiers and quoted dependency/include block labels.

- [ ] **Step 1: Write failing reference and rename tests**

Use this fixture:

```hcl
dependency "app" {
  config_path = "../app"
}

include "root" {
  path = find_in_parent_folders("root.hcl")
}

inputs = {
  id = dependency.app.outputs.id
  x  = include.root.inputs.x
}
```

For each symbol, assert references return the declaration only when `IncludeDeclaration` is true. Assert prepare-rename returns the bare label range, and rename from `app` to `service` produces exactly two edits: declaration text `"service"` and traversal text `service`. Add the equivalent assertions for `include.root`.

- [ ] **Step 2: Run references/rename tests and verify they fail**

Run: `go test ./internal/tg/symbol ./internal/tg/references ./internal/tg/rename ./internal/tg -run 'Dependency|Include' -count=1`

Expected: FAIL because existing rename supports locals only and does not quote block labels.

- [ ] **Step 3: Generalize rename targets and occurrence edits**

Replace string contexts with `symbol.Kind` while keeping the existing identifier validation. For every occurrence:

```go
newText := newName
if occurrence.Declaration && (target.Kind == symbol.Dependency || target.Kind == symbol.Include) {
	newText = strconv.Quote(newName)
}
edits = append(edits, protocol.TextEdit{Range: occurrence.Range, NewText: newText})
```

The shared symbol declaration range must cover the quoted label for the edit, while prepare-rename must expose the inner bare-label range. Store both ranges explicitly in `symbol.Target` as `Range` and `EditRange` rather than calculating them with string offsets.

- [ ] **Step 4: Return one atomic workspace edit**

Sort edits by range, return one `protocol.WorkspaceEdit` entry for the current URI, and return `nil` for invalid identifiers or unsupported positions. References must use the same occurrence list and declaration filter.

- [ ] **Step 5: Run rename/reference and full tests**

Run: `go test ./internal/tg/symbol ./internal/tg/references ./internal/tg/rename ./internal/tg -run 'Rename|References' -count=1`

Expected: PASS.

Run: `go test ./... -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the refactoring checkpoint if commits are authorized**

```bash
git add internal/tg/symbol internal/tg/references internal/tg/rename internal/tg/state.go internal/tg/state_rename_test.go
git commit -m "feat: rename dependency and include symbols"
```

### Task 7: Precise Semantic Diagnostics

**Files:**
- Create: `internal/tg/diagnostics/diagnostics.go`
- Create: `internal/tg/diagnostics/diagnostics_test.go`
- Modify: `internal/tg/parse.go`
- Modify: `internal/tg/parse_test.go`
- Modify: `internal/tg/state.go`
- Modify: `internal/tg/state_test.go`

**Interfaces:**
- Consumes: indexed declarations and Task 5 path services.
- Produces: `diagnostics.Validate(filename, source string, st store.Store) []protocol.Diagnostic`.
- Produces: `diagnostics.FilterParser(st store.Store, input []protocol.Diagnostic) []protocol.Diagnostic`.

- [ ] **Step 1: Write failing semantic diagnostic tests**

Add table cases with exact expected sources/messages:

```go
tests := []struct {
	name string
	source string
	want string
}{
	{"missing local", `inputs = { x = local.absent }`, `No local named "absent" exists in this file.`},
	{"missing dependency", `inputs = { x = dependency.absent.outputs.x }`, `No dependency block named "absent" exists in this file.`},
	{"missing include", `inputs = { x = include.absent.inputs.x }`, `No include block named "absent" exists in this file.`},
	{"missing config path", `dependency "app" {}`, `Dependency "app" is missing a config_path attribute.`},
	{"unevaluable config path", `dependency "app" { config_path = dependency.other.outputs.path }`, `Could not evaluate dependency "app" config_path to a concrete string path.`},
	{"missing dependency target", `dependency "app" { config_path = "../missing" }`, `Dependency "app" points to "../missing", but no Terragrunt file was found there.`},
	{"duplicate locals", "locals { a = 1 }\nlocals { b = 2 }", `Only one locals block is allowed per file.`},
}
```

Add cases proving incomplete `local.`, `dependency.`, and `include.` traversals do not report missing symbols, and parser diagnostics claiming `dependency` is unknown are filtered only when the range belongs to a dependency traversal. Add a circular-locals case (`locals { a = local.b; b = local.a }`) that asserts analysis terminates without panic and retains the official parser's cycle diagnostic.

- [ ] **Step 2: Run diagnostic tests and verify they fail**

Run: `go test ./internal/tg/diagnostics ./internal/tg -run Diagnostic -count=1`

Expected: FAIL because semantic validation is absent.

- [ ] **Step 3: Implement semantic validators**

Walk `*hclsyntax.ScopeTraversalExpr` nodes. Validate only traversals with a root and first attribute. Check `local`, `dependency`, and `include` against their indexed declaration scopes. Emit source `Terragrunt`, severity error, and a range covering the missing attribute name. Validate every dependency block for `config_path`, evaluability, and an existing target through the Task 5 path service.

Detect duplicate top-level `locals` blocks by retaining the first block and reporting each later block's `TypeRange`. Do not report individual duplicate attribute names because HCL already owns that parser diagnostic.

- [ ] **Step 4: Merge and filter diagnostics in one state update**

Keep existing Terragrunt/HCL diagnostics, filter only known false positives tied to dependency traversal AST nodes, append semantic diagnostics, then sort by start line, start character, and message. Store the merged diagnostics on the document snapshot so save can republish them consistently.

The incomplete-expression guard is exact: skip missing-symbol validation when the traversal has fewer than two complete steps or the HCL parser did not produce a `TraverseAttr` for the symbol.

- [ ] **Step 5: Run diagnostic, state, and full tests**

Run: `go test ./internal/tg/diagnostics ./internal/tg -run Diagnostic -count=1`

Expected: PASS.

Run: `go test ./... -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the diagnostics checkpoint if commits are authorized**

```bash
git add internal/tg/diagnostics internal/tg/parse.go internal/tg/parse_test.go internal/tg/state.go internal/tg/state_test.go
git commit -m "feat: add Terragrunt semantic diagnostics"
```

### Task 8: Dependency Output Code Action and Command

**Files:**
- Create: `internal/tg/dependency/output.go`
- Create: `internal/tg/dependency/output_test.go`
- Create: `internal/tg/dependency/testdata/fake-terragrunt/main.go`
- Create: `internal/server/dependency_output.go`
- Create: `internal/server/dependency_output_test.go`
- Modify: `internal/server/server.go`
- Modify: `internal/server/handler.go`
- Modify: `internal/server/server_test.go`

**Interfaces:**
- Produces: `dependency.Runner` and `dependency.NewRunner(timeout time.Duration) Runner`.
- Produces: `Runner.Resolve(context.Context, sourceFile, dependencyName string, st store.Store) (dependency.Output, error)`.
- Produces command: `terragrunt.resolveDependencyOutputs`.

- [ ] **Step 1: Write failing fake-Terragrunt tests**

Build the portable Go fixture at `internal/tg/dependency/testdata/fake-terragrunt/main.go` into `t.TempDir()` (append `.exe` on Windows). The fixture writes its working directory and arguments to paths supplied through test-only environment variables, prints compact JSON, and selects success/error/sleep behavior through another environment variable. Name the binary `terragrunt`, prepend its directory to test `PATH` with `t.Setenv`, and assert:

```go
output, err := runner.Resolve(t.Context(), sourcePath, "app", st)
require.NoError(t, err)
assert.JSONEq(t, `{"id":{"value":"123"}}`, string(output.JSON))
assert.Equal(t, targetPath, output.Target)
assert.Equal(t, filepath.Dir(targetPath), readWorkingDirectory(t))
assert.Equal(t, []string{"output", "-json", "--config", targetPath}, readArguments(t))
```

Add independent tests for missing executable, nonzero exit with ANSI stderr, invalid JSON, context cancellation, and a runner constructed with a 10-millisecond timeout. Assert no command test contacts real infrastructure.

- [ ] **Step 2: Run command tests and verify they fail**

Run: `go test ./internal/tg/dependency -count=1`

Expected: FAIL because the runner package does not exist.

- [ ] **Step 3: Implement safe command execution**

Use these types:

```go
type Output struct {
	JSON []byte
	Target string
}

type Runner struct {
	Timeout time.Duration
	LookPath func(string) (string, error)
	CommandContext func(context.Context, string, ...string) *exec.Cmd
}
```

`NewRunner(60*time.Second)` sets `exec.LookPath` and `exec.CommandContext`. `Resolve` uses Task 5 path resolution, requires an existing regular target, creates `context.WithTimeout`, executes:

```go
cmd := r.CommandContext(ctx, binary, "output", "-json", "--config", target)
cmd.Dir = filepath.Dir(target)
combined, err := cmd.CombinedOutput()
```

Strip ANSI sequences only from error text. Limit displayed stderr to 8 KiB. Parse with `json.Unmarshal`, reformat with `json.Indent`, and append a newline. Map backend initialization and nested dependency failures to concise actionable messages while retaining the final useful stderr line.

- [ ] **Step 4: Write failing code-action and show-document tests**

Test that a range inside either a dependency block or `dependency.app.outputs.id` returns one `refactor.rewrite` action whose command is `terragrunt.resolveDependencyOutputs` with `{uri, dependency}`. Test execution with an injected runner, assert the temp file mode is `0600`, assert formatted JSON, and have the test client reply `{success:true}` to `window/showDocument`.

Add a fallback test where `window/showDocument` returns method-not-found and assert a `window/showMessage` notification contains the temporary path.

- [ ] **Step 5: Advertise and route the code action and command**

Add capabilities:

```go
CodeActionProvider: &protocol.CodeActionOptions{
	CodeActionKinds: []protocol.CodeActionKind{protocol.RefactorRewrite},
},
ExecuteCommandProvider: &protocol.ExecuteCommandOptions{
	Commands: []string{ResolveDependencyOutputsCommand},
},
```

Use command arguments:

```go
type dependencyOutputArgs struct {
	URI protocol.DocumentURI `json:"uri"`
	Dependency string `json:"dependency"`
}
```

Create the temporary file with `os.CreateTemp("", "terragrunt-"+safeDependencyName+"-outputs-*.json")`, verify its mode is `0600` on Unix, track its path on `Server`, and remove tracked paths during exit. Run only after execute-command; code-action discovery must never invoke Terragrunt.

- [ ] **Step 6: Run dependency, server, race, and full tests**

Run: `go test -race ./internal/tg/dependency ./internal/server -count=1`

Expected: PASS, including cancellation and correlated `window/showDocument` response.

Run: `go test ./... -count=1`

Expected: PASS.

- [ ] **Step 7: Commit the code-action checkpoint if commits are authorized**

```bash
git add internal/tg/dependency internal/server
git commit -m "feat: resolve Terragrunt dependency outputs"
```

### Task 9: Remove Legacy Response Wrappers and Verify Protocol Results

**Files:**
- Modify: `internal/tg/state.go`
- Modify: all `internal/tg/state_*_test.go`
- Modify: `internal/server/handler.go`
- Delete: `internal/lsp/consts.go`
- Delete: `internal/lsp/initialize.go`
- Delete: `internal/lsp/lsp.go`
- Delete: `internal/lsp/message.go`
- Delete: remaining `internal/lsp/*.go`

**Interfaces:**
- Produces protocol-neutral state results: `*protocol.Hover`, `[]protocol.Location`, `[]protocol.CompletionItem`, `[]protocol.TextEdit`, `*protocol.Range` or prepare-rename object, and `*protocol.WorkspaceEdit`.
- Removes all server response IDs from the semantic state layer.

- [ ] **Step 1: Add a protocol-shape integration test**

Through framed JSON-RPC, open a document and call hover, definition, references, prepareRename, rename, completion, and formatting. Decode each response into the corresponding `go.lsp.dev/protocol` result type and assert no nested `jsonrpc`, `id`, or `result` fields appear inside the actual result.

- [ ] **Step 2: Run the protocol-shape test before refactoring**

Run: `go test ./internal/server -run TestFeatureResponseShapes -count=1`

Expected: PASS if Task 1 already unwraps every legacy response, or FAIL identifying the remaining wrapper. In either case, retain the test as the migration guard.

- [ ] **Step 3: Refactor state methods to return feature results directly**

Remove `id int` parameters. Return `nil` for unsupported hover/definition/rename results and empty slices where the protocol requires arrays. Keep the prepare-rename `{range, placeholder}` shape in `internal/server` as a small local struct because protocol v0.12.0 exposes only `*protocol.Range`.

Update handlers to pass returned values directly to `reply`. Delete `internal/lsp` only after `rg 'internal/lsp' --glob '*.go'` returns no matches.

- [ ] **Step 4: Run protocol and full tests**

Run: `go test ./internal/server ./internal/tg/... -count=1`

Expected: PASS.

Run: `go test ./... -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the cleanup checkpoint if commits are authorized**

```bash
git add internal/server internal/tg internal/lsp
git commit -m "refactor: separate protocol envelopes from features"
```

### Task 10: Zed Native Configuration and Highlighting Parity

**Files:**
- Modify: `zed-extension/src/lib.rs`
- Modify: `zed-extension/languages/terragrunt/config.toml`
- Modify: `zed-extension/languages/terragrunt/highlights.scm`
- Modify: `zed-extension/README.md`

**Interfaces:**
- Consumes native Zed `lsp.terragrunt.binary` settings and worktree `PATH`, matching the server ID in `extension.toml`.
- Produces canonical filename matching and Terragrunt traversal highlighting.

- [ ] **Step 1: Add failing Rust command-resolution tests**

Extract a pure helper and test configured path precedence, configured arguments/environment, PATH fallback, and missing binary:

```rust
#[test]
fn configured_binary_wins() {
    let configured = CommandSettings {
        path: Some("/tmp/local/terragrunt-ls".into()),
        arguments: Some(vec!["--trace".into()]),
        env: None,
    };
    let command = resolve_command(Some(configured), Some("/usr/bin/terragrunt-ls".into())).unwrap();
    assert_eq!(command.command, "/tmp/local/terragrunt-ls");
    assert_eq!(command.args, vec!["--trace"]);
}
```

- [ ] **Step 2: Run Zed tests and verify they fail**

Run: `cargo test --locked --manifest-path zed-extension/Cargo.toml`

Expected: FAIL because `resolve_command` does not exist.

- [ ] **Step 3: Implement native binary selection**

Rename the ignored method argument to `language_server_id` and read settings with `zed::settings::LspSettings::for_worktree(language_server_id.as_ref(), worktree)`. Use configured `binary.path`, arguments, and environment when present; otherwise use `worktree.which("terragrunt-ls")`. Do not add download code.

Set canonical suffixes exactly:

```toml
path_suffixes = [
  "terragrunt.hcl",
  "root.hcl",
  "terragrunt.stack.hcl",
  "terragrunt.values.hcl",
]
```

Add Tree-sitter queries that scope `local`, `dependency`, `include`, and `inputs` roots as `@type` and following traversal members as `@variable`, while preserving the existing outline file unchanged.

- [ ] **Step 4: Document Zed native user configuration**

Show examples using Zed's `file_types` and `lsp.terragrunt-ls.binary.path`. State that the extension checks the configured path before `PATH` and never downloads the binary.

- [ ] **Step 5: Run Zed checks**

Run: `cargo fmt --manifest-path zed-extension/Cargo.toml -- --check`

Expected: PASS.

Run: `cargo test --locked --manifest-path zed-extension/Cargo.toml`

Expected: PASS.

Run: `cargo check --locked --manifest-path zed-extension/Cargo.toml`

Expected: PASS.

- [ ] **Step 6: Commit the Zed checkpoint if commits are authorized**

```bash
git add zed-extension
git commit -m "feat: align Zed Terragrunt support"
```

### Task 11: VS Code Native Configuration and Highlighting Parity

**Files:**
- Modify: `vscode-extension/package.json`
- Modify: `vscode-extension/src/extension.ts`
- Create: `vscode-extension/src/test/manifest.test.ts`
- Modify: `vscode-extension/tsconfig.json`
- Modify: `vscode-extension/syntaxes/terragrunt.tmGrammar.json`
- Modify: `vscode-extension/README.md`

**Interfaces:**
- Uses native VS Code `files.associations` for extra filenames.
- Continues to launch `out/terragrunt-ls` in packaged mode and local `go run` in extension-development mode.

- [ ] **Step 1: Add a failing manifest test**

Add a Node test that reads `package.json` and asserts exact filenames and activation:

```ts
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';

test('claims only canonical Terragrunt filenames', () => {
  const manifest = JSON.parse(fs.readFileSync(path.join(__dirname, '..', '..', 'package.json'), 'utf8'));
  assert.deepEqual(manifest.contributes.languages[0].filenames, [
    'terragrunt.hcl',
    'root.hcl',
    'terragrunt.stack.hcl',
    'terragrunt.values.hcl',
  ]);
  assert.ok(manifest.activationEvents.includes('onLanguage:terragrunt'));
});
```

Add `"test": "node --test out/test/*.test.js"` to scripts.

- [ ] **Step 2: Install declared dependencies and run the test to verify failure**

Run: `npm ci --prefix vscode-extension`

Expected: PASS and create `vscode-extension/node_modules` without changing the lockfile.

Run: `npm run compile --prefix vscode-extension && npm test --prefix vscode-extension`

Expected: FAIL because `root.hcl` and `onLanguage:terragrunt` are absent.

- [ ] **Step 3: Update canonical activation and keep the client thin**

Set the four exact filenames and add activation events:

```json
"activationEvents": [
  "onLanguage:terragrunt",
  "workspaceContains:**/terragrunt.hcl",
  "workspaceContains:**/root.hcl",
  "workspaceContains:**/terragrunt.stack.hcl",
  "workspaceContains:**/terragrunt.values.hcl"
]
```

Keep the document selector language-based so native `files.associations` activates custom files. Narrow the filesystem watcher to `**/{terragrunt.hcl,root.hcl,terragrunt.stack.hcl,terragrunt.values.hcl}` because user-associated open documents are already synchronized by the language client.

Add TextMate traversal scopes for `local`, `dependency`, `include`, and `inputs`. Preserve existing block and attribute patterns.

- [ ] **Step 4: Document VS Code native user configuration**

Add a settings example:

```json
{
  "files.associations": {
    "common.hcl": "terragrunt",
    "*.terragrunt.hcl": "terragrunt"
  }
}
```

State that packaged local development uses `out/terragrunt-ls` and this phase does not download releases.

- [ ] **Step 5: Run VS Code checks**

Run: `npm run compile --prefix vscode-extension`

Expected: PASS.

Run: `npm test --prefix vscode-extension`

Expected: PASS.

Run: `npm run lint --prefix vscode-extension`

Expected: PASS with no errors.

- [ ] **Step 6: Commit the VS Code checkpoint if commits are authorized**

```bash
git add vscode-extension
git commit -m "feat: align VS Code Terragrunt support"
```

### Task 12: Documentation, Cross-Editor Smoke Test, and Final Verification

**Files:**
- Modify: `docs/server-capabilities.md`
- Modify: `docs/setup.md`
- Modify: `README.md`
- Modify: `zed-extension/README.md`
- Modify: `vscode-extension/README.md`
- Create: `docs/editor-smoke-test.md`

**Interfaces:**
- Documents the final server/editor contract and repeatable local verification.

- [ ] **Step 1: Write the operator-facing documentation**

Update capability documentation with hover, definition, references, rename, completion ranges, diagnostics, formatting, code actions, and lifecycle. Document the exact dependency-output command, its explicit-action requirement, `PATH` dependency on the Terragrunt CLI, 60-second timeout, private temporary JSON output, and `window/showDocument` fallback.

Document local build/install commands:

```bash
go build -o ./vscode-extension/out/terragrunt-ls ./
go install ./
npm ci --prefix vscode-extension
npm run compile --prefix vscode-extension
cargo check --locked --manifest-path zed-extension/Cargo.toml
```

State the four canonical filenames and provide native additional-file examples for both editors. Do not describe automatic download or fork-hosted releases as implemented.

- [ ] **Step 2: Add a repeatable cross-editor smoke checklist**

`docs/editor-smoke-test.md` must use one fixture containing nested locals, include, dependency, dependency output traversal, a missing local, and an indented completion prefix. For both editors, verify:

1. Only canonical filenames activate by default.
2. A native user association activates `common.hcl`.
3. Hover shows a nested value.
4. Definition opens local/include/dependency/file targets.
5. References and rename cover local/dependency/include declarations and uses.
6. Completion preserves indentation.
7. Diagnostics report the missing local and clear after correction/close.
8. Formatting still works for unit, stack, and values files.
9. The dependency-output action opens formatted JSON.
10. Shutdown leaves no server process and temporary output is cleaned on exit.

- [ ] **Step 3: Run formatting and static checks**

Run: `gofmt -w main.go internal`

Expected: command exits 0; inspect `git diff` to ensure only intended Go formatting changed.

Run: `go vet ./...`

Expected: PASS.

Run: `git diff --check`

Expected: PASS.

- [ ] **Step 4: Run all automated verification from a clean dependency install**

Run: `go test -race ./... -count=1`

Expected: PASS.

Run: `cargo fmt --manifest-path zed-extension/Cargo.toml -- --check && cargo test --locked --manifest-path zed-extension/Cargo.toml && cargo check --locked --manifest-path zed-extension/Cargo.toml`

Expected: all three commands PASS.

Run: `npm ci --prefix vscode-extension && npm run compile --prefix vscode-extension && npm test --prefix vscode-extension && npm run lint --prefix vscode-extension`

Expected: all four commands PASS and `package-lock.json` remains unchanged.

- [ ] **Step 5: Perform the manual smoke test**

Build the same current source for both editors. Put one copy on `PATH` for Zed and build the other at `vscode-extension/out/terragrunt-ls`. Work through every item in `docs/editor-smoke-test.md` in Zed and VS Code and record the tested editor versions and result at the bottom of the checklist.

Expected: all ten checks pass in both editors with identical semantic results.

- [ ] **Step 6: Review scope and repository state**

Run: `git status --short`

Expected: only files named by this plan are changed; no binaries, `.vsix` archives, `node_modules`, debug logs, temporary JSON, or reference-repository files are tracked.

Run: `git diff --stat`

Expected: changes are limited to server features, tests, two editor integrations, and documentation; there is no release/downloader implementation.

- [ ] **Step 7: Commit the documentation and verification checkpoint if commits are authorized**

```bash
git add README.md docs zed-extension/README.md vscode-extension/README.md
git commit -m "docs: describe editor feature parity"
```

---

## Final Acceptance Gate

Do not report completion until all of these are evidenced in the current checkout:

- `go test -race ./... -count=1` passes.
- Zed formatting, tests, and locked check pass.
- VS Code clean install, compile, tests, and lint pass.
- The cross-editor manual checklist passes against the same server source.
- Canonical default filenames and native user-added filenames behave as specified.
- Dependency output resolution is explicit, shell-free, cancelable, timeout-bound, and tested with a fake executable.
- No release publication, automatic download, generated protocol tree, packaged binary, or temporary artifact entered the diff.
