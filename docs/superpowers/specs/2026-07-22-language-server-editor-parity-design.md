# Terragrunt Language Server and Editor Parity Design

## Status

Approved in conversation on 2026-07-22. This document defines the design only;
implementation planning and code changes follow separately.

## Context

The official `terragrunt-ls` repository already provides the common language
server foundation, including parsing, formatting, completion, hover,
definitions, references, rename, stack-file support, values-file support, and
Zed and VS Code integrations. A separate local implementation demonstrates
additional Terragrunt-aware behavior that is missing from the official server.

The goal is to add all identified missing behavior to the local fork of the
official repository while preserving existing official functionality. Zed and
VS Code must expose the same semantic features through one native
`terragrunt-ls` binary.

## Goals

- Add dependency-output execution through an explicit code action.
- Add richer Terragrunt hover, navigation, references, rename, completion, and
  diagnostics.
- Complete the LSP document and process lifecycle.
- Support server-to-client LSP requests correctly.
- Keep Zed and VS Code behavior aligned by implementing semantics in the server.
- Preserve existing unit, stack, and values-file behavior.
- Recognize only canonical Terragrunt filenames by default while allowing users
  to add filenames through native editor configuration.
- Support local development with a locally built native binary.

## Non-goals

- Publishing a fork or releasing binaries.
- Automatically downloading or updating language-server binaries.
- Adding an extension-specific setting for extra filenames.
- Claiming arbitrary `.hcl` files by default.
- Moving Terragrunt semantic behavior into either editor extension.
- Cross-file or project-wide rename beyond the current Terragrunt file.

## Canonical filenames

Both editor extensions recognize these files by default:

- `terragrunt.hcl`
- `root.hcl`
- `terragrunt.stack.hcl`
- `terragrunt.values.hcl`

Additional filenames are configured through each editor's native mechanism:

- VS Code: `files.associations`
- Zed: `file_types`

The extensions do not define another setting that duplicates these facilities.

## Existing gaps

The implementation covers all gaps identified during comparison with the local
reference implementation:

1. A code action and command for resolving dependency outputs.
2. Definition navigation through dependencies, evaluated paths, and `file(...)`.
3. References and rename for dependency and include symbols.
4. Hover information for nested local traversals.
5. Precise semantic diagnostics for Terragrunt symbols and paths.
6. Completion edits that replace only the typed prefix.
7. Complete document, process, cancellation, and client-request lifecycle.
8. Matching filename associations, syntax support, and local binary behavior in
   Zed and VS Code.

## Considered approaches

### Selective patches to the current dispatcher

This would produce the smallest initial diff, but the current synchronous
dispatcher is not a sound base for concurrent request correlation,
server-to-client requests, cancellation, and graceful shutdown. Implementing
those concerns inline would create fragile protocol code.

### Replace the server with the reference architecture

This would reach feature parity quickly, but it would replace substantial parts
of the official server, introduce a large generated protocol surface, and risk
regressions in official features such as stack and values files. It would also
be harder to review or contribute upstream.

### Bounded hybrid

This is the selected approach. The official repository remains the foundation.
Its parsing and language functionality are retained, while its JSON-RPC
transport is given the bounded restructuring needed for correct protocol
behavior. Missing semantic behavior is then ported into focused official-server
components rather than copying the reference architecture wholesale.

## Architecture

The design has three boundaries.

### JSON-RPC and LSP transport

The transport owns message framing, concurrent request handling, request IDs,
response correlation, cancellation, protocol errors, initialization, shutdown,
exit, and server-to-client calls. It uses the repository's existing Go LSP
protocol types and does not import the reference repository's generated
protocol tree wholesale.

The transport exposes a small client interface for notifications and requests,
including diagnostic publication, messages, and `window/showDocument`.
Language-feature handlers do not write protocol messages directly.

### Language-feature handlers

Focused handlers implement hover, definitions, references, rename, completion,
diagnostics, formatting, code actions, commands, and document lifecycle events.
Handlers consume document snapshots and Terragrunt services and return LSP
results. A handler failure is isolated to its request and cannot terminate the
server.

### Terragrunt services

Terragrunt services provide reusable document analysis, symbol resolution,
expression evaluation, path resolution, and dependency-output execution. These
services have no editor-specific behavior and expose narrow interfaces that can
be tested independently.

## Document and analysis data flow

Open documents are stored as versioned in-memory snapshots.

1. `textDocument/didOpen`, `textDocument/didChange`, and
   `textDocument/didSave` update the snapshot.
2. Analysis parses the current snapshot and builds a reusable symbol model.
3. The model records locals, includes, dependencies, traversals, resolved paths,
   and source ranges.
4. Diagnostics are published for the same document version.
5. Interactive feature requests consume the snapshot and symbol model.
6. Results produced for a stale document version are discarded.
7. `textDocument/didClose` removes the snapshot and clears its diagnostics.

The shared symbol model ensures that hover, definition, references, rename, and
diagnostics apply the same resolution rules.

## Feature behavior

### Hover

Hover resolves both simple and nested local traversals. Unknown, sensitive, or
partially evaluable expressions produce a safe partial result or no result,
never a request failure.

### Definitions

Definition navigation supports:

- local references;
- include references;
- dependency references;
- dependency output traversals;
- evaluated dependency `config_path` expressions;
- paths passed to `file(...)`.

Paths are resolved relative to the declaring Terragrunt file. Valid targets
outside the editor workspace may be opened, but the server never modifies them.

### References and rename

References and rename support local, dependency, and include symbols within the
current Terragrunt file. Rename validates the requested identifier and returns
one atomic workspace edit covering declarations and resolved uses. Project-wide
rename is outside scope.

### Completion

Existing unit, stack, and values-file completions are preserved. Text edits
replace only the prefix already typed at the cursor instead of replacing the
whole line or expression.

### Diagnostics

Diagnostics combine existing parse and Terragrunt diagnostics with semantic
checks for:

- missing local, dependency, and include symbols;
- invalid or unevaluable dependency paths;
- missing dependency targets;
- duplicate local declarations.

Diagnostics avoid false positives for incomplete expressions while the user is
typing. Circular resolution is detected and reported without recursive failure.

### Formatting

The official formatter behavior remains unchanged for unit, stack, and values
files.

### Dependency outputs

The server advertises a code action and execute command for resolving dependency
outputs. The operation runs only after the user explicitly selects the action.

The command flow is:

1. Resolve and validate the dependency configuration path.
2. Resolve `terragrunt` from `PATH`.
3. Start `terragrunt output -json --config <path>` without a shell, using the
   dependency directory as the working directory.
4. Apply request cancellation and a 60-second execution timeout.
5. Require successful execution and valid JSON output.
6. Format the JSON and write it to a private temporary file outside the
   repository.
7. Ask the editor to open it with `window/showDocument`.
8. If that request is unsupported, show the temporary path with
   `window/showMessage`.

Temporary output is informational. The operation never edits configuration or
infrastructure state through the language server.

### Lifecycle

The server handles initialize/initialized, open/change/save/close, request
cancellation, shutdown, and exit according to LSP expectations. It correlates
responses to server-to-client requests and rejects requests received after
shutdown. Logs remain on stderr because stdout is reserved for LSP traffic.

## Error handling and safety

- Invalid or incomplete HCL returns reliable partial results and diagnostics
  instead of crashing handlers.
- Missing, circular, or unevaluable paths produce targeted diagnostics.
- Dependency paths must resolve to an existing regular file before command
  execution.
- Terragrunt is started with an argument array and known working directory;
  user-controlled values are never interpolated into a shell command.
- Cancellation terminates the child process.
- Command stderr is size-limited and summarized for the user. Recognizable
  backend-initialization and nested-dependency failures receive actionable
  explanations.
- Output is rejected unless it is valid JSON.
- Temporary files use private permissions and are removed on server exit where
  possible.
- JSON-RPC failures use appropriate protocol error codes.
- An individual handler, path, client-request, or subprocess failure does not
  terminate the server.

## Editor integrations

### Zed

The Zed extension associates only canonical filenames by default. Users add
other names with `file_types`. The extension resolves the language server from
Zed's configured LSP `binary.path` first and then from `terragrunt-ls` on
`PATH`. Terragrunt traversal highlighting is added while the existing richer
outline behavior is preserved.

### VS Code

The VS Code extension uses its locally bundled native `terragrunt-ls` binary and
canonical language patterns. Users add filenames through `files.associations`.
Its TextMate grammar is extended only where needed to match Terragrunt traversal
highlighting in Zed.

Neither extension implements semantic language behavior or automatically
downloads a server in this phase.

## Testing strategy

### Unit tests

Unit tests cover symbol indexing, nested traversals, path evaluation, prefix edit
ranges, identifier validation, diagnostic suppression, circular resolution, and
UTF-16 LSP positions.

### Handler tests

Shared Terragrunt fixtures exercise hover, definition, references, rename,
completion, diagnostics, formatting, code actions, commands, and document
lifecycle handlers.

### Protocol integration tests

Framed stdio tests verify concurrent request correlation, cancellation,
shutdown/exit, protocol errors, diagnostic publication, and
`window/showDocument` request/response behavior.

### Command tests

A fake `terragrunt` executable verifies arguments, working directory, successful
JSON formatting, missing executables, invalid JSON, nonzero exits, stderr
summaries, timeouts, cancellation, and temporary-file cleanup. Tests do not
access real infrastructure.

### Editor checks

- Run the full Go test suite.
- Run Zed's locked Cargo checks and applicable extension tests.
- Install VS Code's declared dependencies, then run its compilation and tests.
- Validate both extension manifests and packaged file layouts.
- Perform manual smoke tests in Zed and VS Code against the same locally built
  native binary.

## Acceptance criteria

- All identified language-server features work in both Zed and VS Code through
  the same native server implementation.
- Existing unit, stack, values, formatting, outline, and completion behavior
  remains functional.
- Dependency outputs run only after an explicit code action and produce a safely
  opened JSON document or an actionable error.
- The LSP lifecycle and server-to-client request path pass integration tests.
- Only the four canonical filenames are claimed by default.
- Native user file associations activate Terragrunt support for additional
  filenames without server or extension-specific configuration changes.
- Both extension projects compile and their relevant automated checks pass.
- No release publication or automatic binary download is introduced.

## Delivery boundary

This design targets a local fork and local editor development. A later,
separately designed phase may add fork-hosted releases and automatic binary
downloads after the server features and editor parity are verified.
