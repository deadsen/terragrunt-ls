# Terragrunt for Zed

[Zed extension](https://zed.dev/docs/extensions/installing-extensions) for [terragrunt-ls](https://github.com/gruntwork-io/terragrunt-ls), mostly based on [terraform extension](https://github.com/zed-extensions/terraform)

It exposes the server's hover, definition, references, rename, completion, diagnostics, formatting, and dependency-output action features. See the repository's [capability reference](../docs/server-capabilities.md) for details.

## Configuration

The extension recognizes Terragrunt's canonical filenames:

- `terragrunt.hcl`
- `root.hcl`
- `terragrunt.stack.hcl`
- `terragrunt.values.hcl`

To recognize additional filenames, configure Zed's native `file_types` setting. Because this setting replaces the extension defaults, retain the canonical filenames alongside your additions:

```json
{
  "file_types": {
    "Terragrunt": [
      "terragrunt.hcl",
      "root.hcl",
      "terragrunt.stack.hcl",
      "terragrunt.values.hcl",
      "*.terragrunt.hcl"
    ]
  }
}
```

The extension resolves the language server in this order:

1. the native LSP binary setting;
2. `terragrunt-ls` on the worktree `PATH`;
3. the latest non-prerelease binary from
   [deadsen/terragrunt-ls releases](https://github.com/deadsen/terragrunt-ls/releases).

Downloaded binaries are matched to the current operating system and
architecture, verified against the release's `SHA256SUMS`, and cached in the
extension work directory. To use a local build or fork instead, configure the
native LSP binary setting:

```json
{
  "lsp": {
    "terragrunt": {
      "binary": {
        "path": "/absolute/path/to/terragrunt-ls",
        "arguments": ["--trace"],
        "env": {
          "TG_LOG": "debug"
        }
      }
    }
  }
}
```

A configured binary path takes precedence over both `PATH` and the managed
download.

For a repeatable local verification flow, use the [cross-editor smoke test](../docs/editor-smoke-test.md).
