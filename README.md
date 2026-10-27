# apisdkgen - Go API SDK generator

[![Go Reference](https://pkg.go.dev/badge/github.com/maruel/apisdkgen.svg)](https://pkg.go.dev/github.com/maruel/apisdkgen)

Generate typed TypeScript, Kotlin, and Swift SDKs and an API reference from Go
DTOs and route definitions.

Define an `apispec.Config` for your routes, then call `apisdkgen.Generate` from
your application's generation command. Keep the specification in sync with
the routes your server exposes. See the Go Reference for configuration and
output options.

Run `make test-deps` to install pinned TypeScript, Kotlin, Swift (on Linux),
and JVM libraries in the ignored `.test-deps` directory. The installer needs
Java, Node/npm, curl, GnuPG, tar, and unzip. Swiftly reports any missing system libraries.
`make test` installs missing dependencies, sets the compiler paths and Kotlin
classpath, and runs the complete suite without the Go test cache. The Swift
transport fixture runs on Linux.

Generated-output tests compare complete results with golden files in
`testdata/golden`. To update expectations after an intentional output change:

```sh
go test ./... -run 'Test(DocRegistry|Generate|WriteRoute)' -update
git diff -- testdata/golden
```

Review the golden diff, then run `make test` without `-update`.
