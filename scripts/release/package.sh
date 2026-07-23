#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
# shellcheck source=scripts/release/common.sh
source "$SCRIPT_DIR/common.sh"

tag="${1:-}"
version="$(release_version "$tag")"
dist="$REPO_ROOT/dist"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

"$SCRIPT_DIR/validate-version.sh" "$tag"
mkdir -p "$dist"
find "$dist" -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +
npm ci --prefix "$REPO_ROOT/vscode-extension"

build_target() {
  local goos="$1"
  local goarch="$2"
  local vsce_target="$3"
  local archive="$4"
  local vsix="$5"
  local target_dir="$work/${goos}_${goarch}"
  mkdir -p "$target_dir"

  (
    cd "$REPO_ROOT"
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
      go build -trimpath -ldflags '-s -w' \
        -o "$target_dir/terragrunt-ls" .
  )
  chmod 0755 "$target_dir/terragrunt-ls"
  tar -C "$target_dir" -czf "$dist/$archive" terragrunt-ls

  (
    cd "$REPO_ROOT/vscode-extension"
    GOOS="$goos" GOARCH="$goarch" \
      npx --no-install vsce package \
        --target "$vsce_target" \
        --out "$dist/$vsix"
  )
}

build_target linux amd64 linux-x64 \
  "terragrunt-ls_${version}_linux_amd64.tar.gz" \
  "terragrunt-ls-${version}-linux-x64.vsix"
build_target linux arm64 linux-arm64 \
  "terragrunt-ls_${version}_linux_arm64.tar.gz" \
  "terragrunt-ls-${version}-linux-arm64.vsix"
build_target darwin arm64 darwin-arm64 \
  "terragrunt-ls_${version}_darwin_arm64.tar.gz" \
  "terragrunt-ls-${version}-darwin-arm64.vsix"

DIST_DIR="$dist" "$SCRIPT_DIR/package-zed.sh" "$tag"

"$SCRIPT_DIR/checksums.sh" "$tag"
"$SCRIPT_DIR/verify-package.sh" "$tag"
