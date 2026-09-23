# Build, format, and test the standalone API SDK generator.
.DEFAULT_GOAL := verify
.PHONY: custom-gcl fix verify test

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

verify: custom-gcl
	./custom-gcl config verify
	./custom-gcl run ./...
	go mod tidy -diff
	go test -run '^$$' ./...

test:
	go test ./...
