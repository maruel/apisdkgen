# apisdkgen

Generates TypeScript, Kotlin, and Swift SDKs and API reference documentation from Go DTOs and route definitions. The generator lives in this independent Go module; each application owns its generation command and output directories.

## SDK specifications

An application can define an `SDKAPI() apispec.Config[ErrorCode]` function in its API package and call `apisdkgen.Generate(apisdkgen.NewAPI(sourceDir, output, SDKAPI()))` from its command. `sourceDir` points to the DTO package source; `OutputConfig` names output directories.

`ClientScopes` groups endpoints into bound client factories; `QueryFromReq` serializes typed GET request fields as URL parameters. JSON tags determine which fields appear in generated DTOs. Embedded value structs from `SDKPackagePaths` are flattened using Go's JSON field precedence. Embedded pointers are rejected because nil pointers omit their fields; embed by value or give the field a JSON name to keep it nested. Add other embedded struct packages to `SDKPackagePaths`, or give those fields a JSON name. `SpecialTypes` maps Go types with custom JSON representations. Consumers should keep route specifications in sync with their routers.

The `tool` directive in `go.mod` pins golangci-lint; `go tool` downloads it automatically without a separate installation step. `make verify` builds a cached custom linter binary with the shared `commentcheck` and `methodfilecheck` plugins (declared in `.custom-gcl.yml`). Run `make fix` to format, `make verify` for static analysis, formatting and module checks, and `make test` for unit tests. CI runs the same verification and tests.
