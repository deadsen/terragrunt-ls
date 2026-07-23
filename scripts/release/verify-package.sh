#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
# shellcheck source=scripts/release/common.sh
source "$SCRIPT_DIR/common.sh"

version="$(release_version "${1:-}")"
dist="${DIST_DIR:-$REPO_ROOT/dist}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

release_all_names "$version" | sort > "$tmp/expected"
for path in "$dist"/*; do basename "$path"; done | sort > "$tmp/actual"
diff -u "$tmp/expected" "$tmp/actual"

verify_binary() {
  local binary="$1"
  local target="$2"
  local info
  info="$(file "$binary")"
  case "$target" in
    linux_amd64) grep -Eq 'ELF 64-bit LSB.*x86-64.*statically linked' <<<"$info" ;;
    linux_arm64) grep -Eq 'ELF 64-bit LSB.*(ARM aarch64|ARM64).*statically linked' <<<"$info" ;;
    darwin_arm64) grep -Eq 'Mach-O 64-bit.*arm64' <<<"$info" ;;
    *) echo "unsupported verification target $target" >&2; return 1 ;;
  esac
}

for target in linux_amd64 linux_arm64 darwin_arm64; do
  archive="$dist/terragrunt-ls_${version}_${target}.tar.gz"
  [[ "$(tar -tzf "$archive")" == "terragrunt-ls" ]]
  mkdir "$tmp/$target"
  tar -xzf "$archive" -C "$tmp/$target"
  verify_binary "$tmp/$target/terragrunt-ls" "$target"
done

for mapping in \
  "linux-x64:linux_amd64" \
  "linux-arm64:linux_arm64" \
  "darwin-arm64:darwin_arm64"; do
  vsce_target="${mapping%%:*}"
  binary_target="${mapping##*:}"
  vsix="$dist/terragrunt-ls-${version}-${vsce_target}.vsix"
  embedded="$tmp/${vsce_target}-terragrunt-ls"
  unzip -p "$vsix" extension/out/terragrunt-ls > "$embedded"
  verify_binary "$embedded" "$binary_target"
done

zed="$dist/terragrunt-ls-zed-${version}.zip"
for required in \
  zed-extension/extension.toml \
  zed-extension/Cargo.toml \
  zed-extension/Cargo.lock \
  zed-extension/src/lib.rs \
  zed-extension/languages/terragrunt/config.toml; do
  unzip -Z1 "$zed" | grep -Fx "$required" >/dev/null
done
if unzip -Z1 "$zed" | grep -Eq '(^|/)target/|\.wasm$'; then
  echo "Zed archive contains build output" >&2
  exit 1
fi

checksum="$(release_checksum_name "$version")"
(
  cd "$dist"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum -c "$checksum"
  else
    shasum -a 256 -c "$checksum"
  fi
)

echo "verified release package $version"
