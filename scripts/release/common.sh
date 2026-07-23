#!/usr/bin/env bash

release_version() {
  local tag="${1:-}"
  if [[ ! "$tag" =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
    echo "invalid release tag '$tag'; expected vMAJOR.MINOR.PATCH" >&2
    return 1
  fi
  printf '%s.%s.%s\n' "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}" "${BASH_REMATCH[3]}"
}

release_assert_version() {
  local expected="$1"
  local label="$2"
  local actual="$3"
  if [[ "$actual" != "$expected" ]]; then
    echo "$label version '$actual' does not match release version '$expected'" >&2
    return 1
  fi
}

release_json_version() {
  node -e 'const fs=require("fs"); console.log(JSON.parse(fs.readFileSync(process.argv[1], "utf8")).version)' "$1"
}

release_toml_version() {
  awk -F ' = ' '/^version = "/ {gsub(/"/, "", $2); print $2; exit}' "$1"
}

release_sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

release_payload_names() {
  local version="$1"
  printf '%s\n' \
    "terragrunt-ls_${version}_linux_amd64.tar.gz" \
    "terragrunt-ls_${version}_linux_arm64.tar.gz" \
    "terragrunt-ls_${version}_darwin_arm64.tar.gz" \
    "terragrunt-ls-${version}-linux-x64.vsix" \
    "terragrunt-ls-${version}-linux-arm64.vsix" \
    "terragrunt-ls-${version}-darwin-arm64.vsix" \
    "terragrunt-ls-zed-${version}.zip"
}

release_checksum_name() {
  printf 'terragrunt-ls_%s_SHA256SUMS\n' "$1"
}

release_all_names() {
  release_payload_names "$1"
  release_checksum_name "$1"
}
