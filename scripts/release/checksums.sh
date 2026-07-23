#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
# shellcheck source=scripts/release/common.sh
source "$SCRIPT_DIR/common.sh"

version="$(release_version "${1:-}")"
dist="${DIST_DIR:-$REPO_ROOT/dist}"
checksum_name="$(release_checksum_name "$version")"
: > "$dist/$checksum_name"

while IFS= read -r name; do
  [[ -f "$dist/$name" ]] || { echo "missing payload $name" >&2; exit 1; }
  printf '%s  %s\n' "$(release_sha256 "$dist/$name")" "$name" >> "$dist/$checksum_name"
done < <(release_payload_names "$version")
