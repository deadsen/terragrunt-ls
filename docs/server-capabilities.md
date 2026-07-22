# Server capabilities

`terragrunt-ls` implements the following Language Server Protocol features.

## Document lifecycle and diagnostics

The server uses full-text synchronization and handles open, change, save, and close notifications. It ignores stale document versions, re-analyzes the current buffer on save, publishes parser and Terragrunt-aware diagnostics, and clears diagnostics and in-memory state when a document closes.

Semantic diagnostics cover missing local, dependency, and include references; missing or unevaluable dependency `config_path` values; dependency paths without a Terragrunt target; and duplicate `locals` blocks. Parser diagnostics are retained except for dependency-traversal false positives proven by the syntax tree.

## Hover

Hovering a `local.<name>` traversal shows the evaluated local value as HCL. Nested paths such as `local.service.metadata.owner` resolve to the selected nested value, and the response range covers the selected member.

## Definition

Go to Definition resolves:

- local references to their declaration in the current document;
- include declarations and references to the evaluated include file;
- dependency declarations and references to the dependency's Terragrunt configuration;
- `file(...)` calls with a locally evaluable string path to the referenced file.

## References and rename

Find References and Rename share a Terragrunt symbol model for local names, dependency labels, and include labels. They operate across declarations and uses in the current unit document. Prepare Rename returns the exact identifier range and placeholder; rename validates HCL identifiers and preserves quotes around dependency and include labels. Stack and values files do not expose rename or references.

## Completion

Completion provides context-specific Terragrunt blocks and attributes for unit and stack files. Each completion returns a text edit for only the identifier prefix immediately before the cursor, preserving any leading indentation. Values files intentionally return no Terragrunt structural completions.

## Formatting

Document formatting applies canonical HCL formatting to the complete current buffer. It is available for unit, stack, and values files.

## Dependency output action

The `refactor.rewrite` code action **Resolve outputs for dependency "<name>"** is offered when the cursor is on a dependency block or dependency traversal. Outputs are never fetched during ordinary parsing, hover, completion, or diagnostics; the user must explicitly invoke the action.

The action executes the command `terragrunt output -json --config <target>` directly, without a shell, with the dependency target's directory as its working directory. The Terragrunt CLI must be available on the language server's `PATH`. Execution is cancelable and has a 60-second timeout.

Valid JSON output is formatted and written to a private temporary file with mode `0600`. The server asks the editor to open it with `window/showDocument`; if that request fails or is declined, it reports the path with `window/showMessage`. Tracked output files are removed when the client sends `exit`.

The underlying command identifier is `terragrunt.resolveDependencyOutputs`.

## Supported filenames

The server determines parsing behavior from the document basename:

- `terragrunt.stack.hcl` is a stack file;
- `terragrunt.values.hcl` is a values file;
- `terragrunt.hcl`, `root.hcl`, and user-associated filenames are unit files.

Editor activation defaults are exactly `terragrunt.hcl`, `root.hcl`, `terragrunt.stack.hcl`, and `terragrunt.values.hcl`. See [Setup](./setup.md) for native additional-file configuration.
