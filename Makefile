SHELL := /bin/bash
TAG ?=

.PHONY: test test-go test-vscode test-zed test-release release-validate release-package release-package-zed release-checksums

test: test-go test-vscode test-zed test-release

test-go:
	go test ./...

test-vscode:
	npm ci --prefix vscode-extension
	npm run compile --prefix vscode-extension
	npm test --prefix vscode-extension
	npm run lint --prefix vscode-extension

test-zed:
	cargo test --locked --manifest-path zed-extension/Cargo.toml

test-release:
	shellcheck scripts/release/*.sh
	bash scripts/release/version_test.sh
	bash scripts/release/package_contract_test.sh
	bash scripts/release/toolchain_test.sh
	bash scripts/release/zed_package_test.sh

release-validate:
	@test -n "$(TAG)" || { echo "TAG is required" >&2; exit 1; }
	scripts/release/validate-version.sh "$(TAG)"

release-package:
	@test -n "$(TAG)" || { echo "TAG is required" >&2; exit 1; }
	scripts/release/package.sh "$(TAG)"

release-package-zed:
	@test -n "$(TAG)" || { echo "TAG is required" >&2; exit 1; }
	scripts/release/package-zed.sh "$(TAG)"

release-checksums:
	@test -n "$(TAG)" || { echo "TAG is required" >&2; exit 1; }
	scripts/release/checksums.sh "$(TAG)"
	scripts/release/verify-package.sh "$(TAG)"
