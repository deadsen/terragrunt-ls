#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
# shellcheck source=scripts/release/common.sh
source "$SCRIPT_DIR/common.sh"

current_version="$(release_json_version "$REPO_ROOT/vscode-extension/package.json")"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_equal() {
  local expected="$1"
  local actual="$2"
  local label="$3"
  [[ "$actual" == "$expected" ]] || fail "$label: expected '$expected', got '$actual'"
}

assert_equal "$current_version" "$(release_version "v$current_version")" "stable tag"

for invalid in "$current_version" "v${current_version%.*}" "v$current_version-rc1" "v$current_version+build" "release-$current_version"; do
  if release_version "$invalid" >/dev/null 2>&1; then
    fail "accepted invalid tag $invalid"
  fi
done

release_assert_version "$current_version" "VS Code manifest" "$current_version"
if release_assert_version "$current_version" "Zed manifest" "$current_version-mismatch" >/dev/null 2>&1; then
  fail "accepted mismatched version"
fi

"$SCRIPT_DIR/validate-version.sh" "v$current_version" >/dev/null

echo "release version tests passed"
