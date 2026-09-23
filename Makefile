# Build, format, and test the standalone API SDK generator.
.DEFAULT_GOAL := verify
.PHONY: fix verify test

fix:
	goimports -w . apispec

verify:
	test -z "$$(gofmt -l .)"
	go vet ./...
	go test -run '^$$' ./...

test:
	go test ./...
