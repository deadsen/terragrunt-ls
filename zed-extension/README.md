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

The extension uses `terragrunt-ls` from the worktree `PATH` by default. To use a local build or fork, configure the native LSP binary setting:

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

A configured binary path takes precedence over `PATH`. The extension never downloads a language-server binary.

For a repeatable local verification flow, use the [cross-editor smoke test](../docs/editor-smoke-test.md).
