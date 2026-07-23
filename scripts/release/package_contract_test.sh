#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/release/common.sh
source "$SCRIPT_DIR/common.sh"

expected="$(printf '%s\n' \
  terragrunt-ls_0.1.0_linux_amd64.tar.gz \
  terragrunt-ls_0.1.0_linux_arm64.tar.gz \
  terragrunt-ls_0.1.0_darwin_arm64.tar.gz \
  terragrunt-ls-0.1.0-linux-x64.vsix \
  terragrunt-ls-0.1.0-linux-arm64.vsix \
  terragrunt-ls-0.1.0-darwin-arm64.vsix \
  terragrunt-ls-zed-0.1.0.zip \
  terragrunt-ls_0.1.0_SHA256SUMS)"
actual="$(release_all_names 0.1.0)"
[[ "$actual" == "$expected" ]]
echo "release artifact contract tests passed"
