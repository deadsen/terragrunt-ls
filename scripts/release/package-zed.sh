#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
# shellcheck source=scripts/release/common.sh
source "$SCRIPT_DIR/common.sh"

tag="${1:-}"
version="$(release_version "$tag")"
dist="${DIST_DIR:-$REPO_ROOT/dist}"
archive="$dist/terragrunt-ls-zed-${version}.zip"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

"$SCRIPT_DIR/validate-version.sh" "$tag"

mkdir -p "$work/zed-extension" "$dist"
cp -R "$REPO_ROOT/zed-extension/." "$work/zed-extension/"
rm -rf "$work/zed-extension/target" "$work/zed-extension/grammars"
find "$work/zed-extension" -type f \( -name '*.wasm' -o -name '.DS_Store' \) -delete
cp "$REPO_ROOT/LICENSE" "$work/zed-extension/LICENSE"
rm -f "$archive"

(
  cd "$work"
  zip -qr "$archive" zed-extension
)

echo "Created $archive"
