# Setup

## Build dependencies

The repository's supported tool versions are declared in [`mise.toml`](../mise.toml). Install them with [mise](https://mise.jdx.dev/):

```bash
mise install
```

You can also install the listed Go, Node.js, and Rust toolchains manually.

To install a packaged release of this fork instead of building from source,
follow the [distribution guide](./distribution.md).

## Build the language server

From the repository root, install the current source on your `PATH` or build a local binary:

```bash
go install ./
go build -o ./terragrunt-ls ./
```

The editor integrations described here use local binaries. They do not download a language-server release at runtime.

## Canonical filenames and additional files

Both editor extensions recognize only these Terragrunt filenames by default:

- `terragrunt.hcl`
- `root.hcl`
- `terragrunt.stack.hcl`
- `terragrunt.values.hcl`

An additional file opened with language ID `terragrunt` is treated as a unit file.

## Visual Studio Code

Install the locked dependencies, compile the client, and build the server where the packaged extension expects it:

```bash
npm ci --prefix vscode-extension
npm run compile --prefix vscode-extension
go build -o ./vscode-extension/out/terragrunt-ls ./
```

Open `vscode-extension` in VS Code and press F5 to use the Extension Development Host. Development mode runs the repository source with `go run`; packaged mode launches `vscode-extension/out/terragrunt-ls`.

Use VS Code's native file associations for additional filenames:

```json
{
  "files.associations": {
    "common.hcl": "terragrunt",
    "*.terragrunt.hcl": "terragrunt"
  }
}
```

To create a locally installable VSIX, install `@vscode/vsce`, run `npm run vscode:prepublish` in `vscode-extension`, then run `vsce package` there.

## Zed

Verify the extension crate:

```bash
cargo check --locked --manifest-path zed-extension/Cargo.toml
```

Install `zed-extension` as a [development extension](https://zed.dev/docs/extensions/developing-extensions#developing-an-extension-locally).
The extension uses a configured binary first, then searches the worktree
`PATH`, and otherwise downloads the latest matching release binary. The
download is verified with `SHA256SUMS` and cached in Zed's extension work
directory.

To force a local build or fork, configure its absolute path:

```json
{
  "lsp": {
    "terragrunt": {
      "binary": {
        "path": "/absolute/path/to/terragrunt-ls"
      }
    }
  }
}
```

Zed's `file_types` setting replaces the extension defaults, so retain the four canonical filenames when adding a custom one:

```json
{
  "file_types": {
    "Terragrunt": [
      "terragrunt.hcl",
      "root.hcl",
      "terragrunt.stack.hcl",
      "terragrunt.values.hcl",
      "common.hcl"
    ]
  }
}
```

## Neovim

The repository also contains a Neovim plugin. Install `terragrunt-ls` on `PATH`, add this repository as a plugin, and call `require('terragrunt-ls').setup()`. Editor feature parity in this change is scoped to Zed and Visual Studio Code.

## Verification

See the repeatable [cross-editor smoke test](./editor-smoke-test.md) after building both integrations from the same source checkout.
