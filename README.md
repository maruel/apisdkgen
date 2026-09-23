# apisdkgen

Generates TypeScript, Kotlin, and Swift SDKs and API reference documentation from Go DTOs and route definitions. The generator lives in this independent Go module; each application owns its generation command and output directories.

## Local development

Use a workspace while developing alongside caic or mddb (the workspace files are ignored):

```sh
cd ../caic && go work init . ../apisdkgen
cd ../mddb && go work init . ../apisdkgen
cd ../mddb && go work edit -replace=cloud.google.com/go=cloud.google.com/go@v0.123.0
```

The mddb workspace-only replacement avoids an ambiguous `compute/metadata` import from an old transitive dependency in the combined workspace graph; it does not change mddb's module requirements.

The consumer modules intentionally do not pin an unpublished version of `github.com/maruel/apisdkgen`. Once released, add a tagged module requirement in each consumer and remove the local workspace.

```sh
cd caic && go generate ./backend/internal/server/api/v1
cd mddb && go generate ./backend/internal/server
```

## SDK specifications

An application can define an `SDKAPI() apispec.Config[ErrorCode]` function in its API package and call `apisdkgen.Generate(apisdkgen.NewAPI(sourceDir, output, SDKAPI()))` from its command. `sourceDir` points to the DTO package source; `OutputConfig` names output directories.

mddb declares its JSON routes in `backend/internal/server/dto/sdk.go` and generates the TypeScript client and API reference with the same `apispec` pipeline as caic. `ClientScopes` groups workspace and organization endpoints into bound client factories; `QueryFromReq` serializes typed GET request fields as URL parameters. mddb uses `TypeScriptClientOnly` because tygo separately generates its TypeScript DTO types. Keep the route specification in sync with the router (mddb tests compare their route sets).

The `tool` directive in `go.mod` pins golangci-lint; `go tool` downloads it automatically without a separate installation step. `make verify` builds a cached custom linter binary with the shared `commentcheck` and `methodfilecheck` plugins (declared in `.custom-gcl.yml`). Run `make fix` to format, `make verify` for static analysis, formatting and module checks, and `make test` for unit tests. CI runs the same verification and tests.
