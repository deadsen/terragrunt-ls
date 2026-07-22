# Language Server for Terragrunt

This is the official Language Server for [Terragrunt](https://terragrunt.gruntwork.io/).

## Functionality

See the [Language Server README](https://github.com/gruntwork-io/terragrunt-ls) for a full list of features.

The extension exposes full document lifecycle and diagnostics, nested-local hover, definition navigation for locals/includes/dependencies/files, references and rename, range-aware completion, formatting, and an explicit action for resolving dependency outputs. See the [server capability reference](../docs/server-capabilities.md) for the complete contract.

## File names

The extension recognizes Terragrunt's canonical filenames by default:

- `terragrunt.hcl`
- `root.hcl`
- `terragrunt.stack.hcl`
- `terragrunt.values.hcl`

Use VS Code's native `files.associations` setting for additional Terragrunt files:

```json
{
  "files.associations": {
    "common.hcl": "terragrunt",
    "*.terragrunt.hcl": "terragrunt"
  }
}
```

The language-based document selector also enables language-server features for these user-associated files.

## Language server binary

The packaged extension launches its bundled `out/terragrunt-ls` binary. Extension development mode runs the local source with `go run`. The extension does not download language-server releases at runtime.

For a repeatable local verification flow, use the [cross-editor smoke test](../docs/editor-smoke-test.md).

<!-- This README.md is displayed in the extension installation page, so try to keep the docs useful when user facing. -->

## Development

If you are reading this in the Git repository, you can find instructions on how to set up your local development environment and run the extension in development mode in the [Development README](./docs/development.md).
