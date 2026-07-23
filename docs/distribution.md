# Public distribution

Versioned packages are published on the
[deadsen/terragrunt-ls Releases page](https://github.com/deadsen/terragrunt-ls/releases).
This guide distributes the fork through GitHub Release assets and does not
require publishing to the VS Code Marketplace or Zed Extension Gallery.
Packages installed from those assets do not update automatically.

This is an unofficial fork. Its editor identities are
`deadsen.terragrunt-ls` for VS Code and `terragrunt-ls-deadsen` for Zed. Do not
install both the upstream and fork extension in the same editor because both
can activate for Terragrunt files.

## Supported platforms

| Platform | Server archive | VS Code extension |
| --- | --- | --- |
| Linux x64 | `terragrunt-ls_<version>_linux_amd64.tar.gz` | `terragrunt-ls-<version>-linux-x64.vsix` |
| Linux ARM64 | `terragrunt-ls_<version>_linux_arm64.tar.gz` | `terragrunt-ls-<version>-linux-arm64.vsix` |
| macOS ARM64 | `terragrunt-ls_<version>_darwin_arm64.tar.gz` | `terragrunt-ls-<version>-darwin-arm64.vsix` |

The Zed source archive is `terragrunt-ls-zed-<version>.zip` for every supported
platform. Zed also needs the matching standalone server archive.

## Verify a download

Download the selected artifact and
`terragrunt-ls_<version>_SHA256SUMS` from the same release. On Linux:

```bash
grep 'terragrunt-ls_0.1.0_linux_amd64.tar.gz' terragrunt-ls_0.1.0_SHA256SUMS | sha256sum -c -
```

On macOS ARM64:

```bash
grep 'terragrunt-ls_0.1.0_darwin_arm64.tar.gz' terragrunt-ls_0.1.0_SHA256SUMS | shasum -a 256 -c -
```

Replace `0.1.0` and the filename with the release being installed.

## Install the VS Code extension

Remove the upstream extension if it is installed:

```bash
code --uninstall-extension Gruntwork.terragrunt-ls
```

Use **Extensions: Install from VSIX** and select the matching `.vsix`, or run:

```bash
code --install-extension terragrunt-ls-0.1.0-linux-x64.vsix
```

VS Code disables auto-update by default for extensions installed from VSIX.
In the Extensions view, confirm that **Auto Update** is unchecked for
`deadsen.terragrunt-ls`. Install a newer VSIX manually when upgrading.

## Install the Zed extension

1. Uninstall the upstream `Terragrunt` extension if it is installed.
2. Extract `terragrunt-ls-zed-<version>.zip`.
3. In Zed's Extensions page, choose **Install Dev Extension** and select the
   extracted `zed-extension` directory.
4. Extract the matching server archive and place `terragrunt-ls` on `PATH`, or
   set `lsp.terragrunt.binary.path` to its absolute path as shown in
   [setup.md](./setup.md#zed).

Install the newer extension bundle and server archive manually when upgrading.

## Build a Zed dev extension without publishing

To build the Zed bundle locally:

```bash
mise exec -- make release-package-zed TAG=v0.1.0
```

This creates `dist/terragrunt-ls-zed-0.1.0.zip`. The tag must match the versions
in the VS Code and Zed manifests.

You can also run the **Release** workflow manually from GitHub Actions. A manual
run builds and tests the Zed WebAssembly module and stores the source bundle in
the `zed-extension` Actions artifact. Because the run is not associated with a
version tag, it does not upload the bundle to a GitHub Release or publish either
editor extension to a marketplace.

## Publish a release

The existing upstream jobs under `.github/workflows/` remain intact: they build
and upload the server and VSIX matrix, and the VS Code workflow retains its
Marketplace publishing job. The release workflow adds one isolated Zed job
that builds its WebAssembly module, runs its tests, packages its source for
**Install Dev Extension**, uploads the bundle to the GitHub Release for a
version tag, and retains the bundle as an Actions artifact. It does not publish
to the Zed Extension Gallery.

Publish a release:

1. Update the version in `vscode-extension/package.json`, its npm lockfile,
   `zed-extension/extension.toml`, and `zed-extension/Cargo.toml`; refresh the
   Cargo lockfile.
2. Run `mise exec -- make test`.
3. Run `mise exec -- make release-package TAG=v0.1.0`, replacing the version.
4. Review, commit, and push the version change.
5. On GitHub, draft a new release using the exact `vMAJOR.MINOR.PATCH` tag and
   target the release commit.
6. Publish the release without attaching the Zed archive. Creating the tag from
   the release UI ensures the release exists when the tag workflows start.
7. Confirm the workflows upload the server, VSIX, and
   `terragrunt-ls-zed-<version>.zip` assets. The Zed archive is also available
   from the `zed-extension` Actions artifact.
   GitHub Release assets uploaded by the build jobs.
9. If you want to publish the exact local eight-file set instead, attach the
   remaining files from `dist/` after the workflows finish, then verify the
   checksum manifest. Do not re-upload the Zed archive under the same name.
