# Format, verify, and test the standalone API SDK generator.
.DEFAULT_GOAL := help
.PHONY: custom-gcl fix git-hooks help test test-deps verify

help:
	@printf '  %-14s - %s\n' 'make verify' 'Run the static checks'
	@printf '  %-14s - %s\n' 'make test' 'Run Go and generated TypeScript, Kotlin, and Swift tests'
	@printf '  %-14s - %s\n' 'make test-deps' 'Install pinned SDK test dependencies'
	@printf '  %-14s - %s\n' 'make fix' 'Format source and refresh the file index'
	@printf '  %-14s - %s\n' 'make git-hooks' 'Install Git hooks'

# The custom binary embeds two module plugins. Cache it by build inputs rather
# than mtime: golangci-lint builds it in a fresh temporary module each time.
custom-gcl:
	@set -eu; \
	version=$$(go list -m -f '{{.Version}}' github.com/golangci/golangci-lint/v2); \
	config=$$(sha256sum .custom-gcl.yml | cut -d' ' -f1); \
	key=$$(printf '%s\n' "$$config" "$$version" "$$(go env GOVERSION)" | sha256sum | cut -d' ' -f1); \
	if [ -x custom-gcl ] && [ -f .custom-gcl.sha ] && [ "$$key" = "$$(cat .custom-gcl.sha)" ]; then exit 0; fi; \
	go tool golangci-lint custom --version "$$version"; \
	printf '%s\n' "$$key" > .custom-gcl.sha

fix:
	go tool golangci-lint fmt
	@go tool shfmt -w scripts/*.sh scripts/hooks/*
	@python3 scripts/update_agents_file_index.py

verify: custom-gcl
	@go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
	./custom-gcl config verify
	./custom-gcl run ./...
	go mod tidy -diff
	go test -run '^$$' ./...
	@files=$$(git ls-files '*.sh' 'scripts/hooks/*'); [ -z "$$files" ] || go tool shfmt -l $$files
	@python3 scripts/lint_binaries.py
	@python3 scripts/update_agents_file_index.py --check
	@git diff --check

test-deps:
	@./scripts/install-test-deps.sh

test: test-deps
	@set -eu; \
	classpath=$$(printf '%s:' "$(CURDIR)"/.test-deps/jars/*.jar); \
	PATH="$(CURDIR)/.test-deps/node_modules/.bin:$(CURDIR)/.test-deps/bin:$$PATH" \
	SWIFTLY_HOME_DIR="$(CURDIR)/.test-deps/swiftly" \
	SWIFTLY_BIN_DIR="$(CURDIR)/.test-deps/bin" \
	SWIFTLY_TOOLCHAINS_DIR="$(CURDIR)/.test-deps/swiftly/toolchains" \
	APISDKGEN_KOTLIN_CLASSPATH="$${classpath%:}" \
	APISDKGEN_KOTLIN_SERIALIZATION_PLUGIN="$(CURDIR)/.test-deps/kotlin/lib/kotlinx-serialization-compiler-plugin.jar" \
	go test -count=1 ./...

git-hooks:
	@./scripts/install-git-hooks.sh
