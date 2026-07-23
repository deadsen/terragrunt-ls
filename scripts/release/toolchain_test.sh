#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
expected_node="24.18.0"

mise_node="$(
  sed -n 's/^node = "\([^"]*\)"$/\1/p' "$ROOT_DIR/mise.toml"
)"
nvm_node="$(tr -d '[:space:]' < "$ROOT_DIR/.nvmrc")"

[[ "$mise_node" == "$expected_node" ]]
[[ "$nvm_node" == "$expected_node" ]]
[[ "$mise_node" == "$nvm_node" ]]

echo "Node toolchain contract tests passed"
