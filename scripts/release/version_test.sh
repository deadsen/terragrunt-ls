#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/release/common.sh
source "$SCRIPT_DIR/common.sh"

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

assert_equal "0.1.0" "$(release_version v0.1.0)" "stable tag"

for invalid in 0.1.0 v0.1 v0.1.0-rc1 v0.1.0+build release-0.1.0; do
  if release_version "$invalid" >/dev/null 2>&1; then
    fail "accepted invalid tag $invalid"
  fi
done

release_assert_version "0.1.0" "VS Code manifest" "0.1.0"
if release_assert_version "0.1.0" "Zed manifest" "0.1.1" >/dev/null 2>&1; then
  fail "accepted mismatched version"
fi

"$SCRIPT_DIR/validate-version.sh" v0.1.0 >/dev/null

echo "release version tests passed"
