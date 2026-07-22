# Cross-editor smoke test

Use this checklist to verify Zed and Visual Studio Code against binaries built from the same checkout. Record actual editor versions and results at the bottom; do not mark the manual gate complete from automated tests alone.

## Build

From the repository root:

```bash
go install ./
go build -o ./vscode-extension/out/terragrunt-ls ./
npm ci --prefix vscode-extension
npm run compile --prefix vscode-extension
cargo check --locked --manifest-path zed-extension/Cargo.toml
```

Install `zed-extension` as a Zed development extension and point `lsp.terragrunt.binary.path` at the freshly installed binary if it is not visible on the worktree `PATH`. Launch the VS Code Extension Development Host from `vscode-extension`; it runs the same repository source with `go run`.

## Fixture

Create one temporary workspace with this layout:

```text
editor-smoke/
├── common.hcl
├── data.json
├── dependency/
│   └── terragrunt.hcl
├── notes.hcl
├── root.hcl
├── terragrunt.hcl
├── terragrunt.stack.hcl
└── terragrunt.values.hcl
```

`root.hcl`:

```hcl
locals { region = "eu-west-1" }
```

`dependency/terragrunt.hcl` should be a local, initialized Terragrunt unit with at least one real output named `endpoint`. Using an existing disposable unit is acceptable; the output action deliberately exercises the real `terragrunt` executable on `PATH`.

`data.json`:

```json
{"enabled":true}
```

`terragrunt.hcl`:

```hcl
locals {
  service = {
    metadata = {
      owner = "platform"
    }
  }
  document = file("data.json")
}

include "root" {
  path = "root.hcl"
}

dependency "app" {
  config_path = "./dependency"
}

inputs = {
  owner    = local.service.metadata.owner
  region   = include.root.locals.region
  endpoint = dependency.app.outputs.endpoint
  broken   = local.does_not_exist
}
```

`common.hcl` contains an indented completion prefix:

```hcl
  dep
```

`notes.hcl` can contain any HCL. It is the negative default-activation case.

`terragrunt.stack.hcl`:

```hcl
unit "app" { source="./dependency" path="app" }
```

`terragrunt.values.hcl`:

```hcl
owner="platform"
```

## Native additional-file settings

For VS Code:

```json
{
  "files.associations": {
    "common.hcl": "terragrunt"
  }
}
```

For Zed, retain the defaults because `file_types` replaces them:

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

## Checklist

Run every item in both editors:

1. Confirm the Terragrunt language activates by default for the four canonical filenames and does not activate for `common.hcl` or `notes.hcl` before adding a user association.
2. Add the native setting above and confirm `common.hcl` activates while `notes.hcl` remains unclaimed.
3. Hover `owner` in `local.service.metadata.owner` and confirm the nested value `"platform"` is shown with only the selected member highlighted.
4. Use Go to Definition on the local traversal, include label/reference, dependency label/reference, and `file("data.json")`; confirm they open the local declaration, `root.hcl`, `dependency/terragrunt.hcl`, and `data.json` respectively.
5. Run Find References and Rename from local, dependency, and include declarations and uses. Confirm declarations and uses are included, quoted labels remain quoted, and unrelated symbols are unchanged.
6. Invoke completion after `  dep` in `common.hcl`. Confirm `dependency` and `dependencies` are offered and accepting one replaces only `dep`, preserving the two leading spaces.
7. Confirm a `Terragrunt` diagnostic reports `local.does_not_exist`. Change it to `local.service`, verify the diagnostic clears, restore it, then close the document and verify diagnostics are cleared.
8. Format `terragrunt.hcl`, `terragrunt.stack.hcl`, and `terragrunt.values.hcl`; confirm valid canonical HCL formatting in all three.
9. On the `app` dependency block or traversal, invoke **Resolve outputs for dependency "app"**. Confirm the editor opens formatted JSON containing `endpoint`. If `window/showDocument` is unavailable, confirm an informational message reports the private temporary path.
10. Close the editor/client cleanly. Confirm no `terragrunt-ls` process remains and the temporary dependency-output JSON file no longer exists.

## Results

| Editor | Version | Checks 1-10 | Notes |
| --- | --- | --- | --- |
| Zed | Not run | Not run | |
| Visual Studio Code | Not run | Not run | |
