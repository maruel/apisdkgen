# apisdkgen

[![Go Reference](https://pkg.go.dev/badge/github.com/maruel/apisdkgen.svg)](https://pkg.go.dev/github.com/maruel/apisdkgen)

Generate typed TypeScript, Kotlin, and Swift SDKs and an API reference from Go
DTOs and route definitions.

Define an `apispec.Config` for your routes, then call `apisdkgen.Generate` from
your application's generation command. Keep the specification in sync with
the routes your server exposes. See the Go Reference for configuration and
output options.
