#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
dist="$(mktemp -d)"
trap 'rm -rf "$dist"' EXIT

DIST_DIR="$dist" "$SCRIPT_DIR/package-zed.sh" v0.2.0

archive="$dist/terragrunt-ls-zed-0.2.0.zip"
[[ -f "$archive" ]]

for required in \
  zed-extension/LICENSE \
  zed-extension/extension.toml \
  zed-extension/Cargo.toml \
  zed-extension/Cargo.lock \
  zed-extension/src/lib.rs \
  zed-extension/languages/terragrunt/config.toml; do
  unzip -Z1 "$archive" | grep -Fx "$required" >/dev/null
done

if unzip -Z1 "$archive" | grep -Eq '(^|/)target/|(^|/)grammars/|\.wasm$'; then
  echo "Zed archive contains build output" >&2
  exit 1
fi

echo "Zed package tests passed"
