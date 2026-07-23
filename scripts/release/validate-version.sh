#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
# shellcheck source=scripts/release/common.sh
source "$SCRIPT_DIR/common.sh"

tag="${1:-}"
version="$(release_version "$tag")"

release_assert_version "$version" "VS Code manifest" \
  "$(release_json_version "$REPO_ROOT/vscode-extension/package.json")"
release_assert_version "$version" "VS Code lockfile" \
  "$(release_json_version "$REPO_ROOT/vscode-extension/package-lock.json")"
release_assert_version "$version" "Zed extension manifest" \
  "$(release_toml_version "$REPO_ROOT/zed-extension/extension.toml")"
release_assert_version "$version" "Zed Cargo manifest" \
  "$(release_toml_version "$REPO_ROOT/zed-extension/Cargo.toml")"

cargo_version="$({
  cargo metadata --locked --no-deps \
    --manifest-path "$REPO_ROOT/zed-extension/Cargo.toml" \
    --format-version 1
} | node -e '
  const fs = require("fs");
  const metadata = JSON.parse(fs.readFileSync(0, "utf8"));
  const pkg = metadata.packages.find((item) => item.name === "terragrunt-ls");
  if (!pkg) process.exit(2);
  console.log(pkg.version);
')"
release_assert_version "$version" "Zed Cargo lockfile" "$cargo_version"

echo "release version $version matches all manifests"
