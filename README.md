# apisdkgen - Go API SDK generator

[![Go Reference](https://pkg.go.dev/badge/github.com/maruel/apisdkgen.svg)](https://pkg.go.dev/github.com/maruel/apisdkgen)

Generate typed TypeScript, Kotlin, and Swift SDKs and an API reference from Go
DTOs and route definitions.

Define an `apispec.Config` for each API version, then register the versions in
one generated package from your application's generation command:

```go
api := apisdkgen.NewAPI("sdk/voicegateway", "VoiceGatewaySDK",
    apisdkgen.NewVersion("v1", "voicegateway/api/v1", v1.SDKAPI),
    apisdkgen.NewVersion("v2", "voicegateway/api/v2", v2.SDKAPI),
)
err := api.Generate()
```

See the [Go documentation](https://pkg.go.dev/github.com/maruel/apisdkgen)
for the generator and specification contracts.

Run `make test-deps` to install pinned TypeScript, Kotlin, Swift (on Linux),
and JVM libraries in the ignored `.test-deps` directory. The installer needs
Java, Node/npm, Python 3, curl, GnuPG, tar, and unzip. Swiftly reports any missing system libraries.
If initial Swiftly setup fails while importing GPG keys on a minimal Linux
installation, install the `gpg-agent` package before retrying.
On Ubuntu, install `libcurl4-openssl-dev` before running the installer;
CI installs this package before `make test-deps`.
`make test` installs missing dependencies, sets the compiler paths and Kotlin
classpath, and runs the complete suite without the Go test cache. Kotlin compiler
and runtime versions are owned by `kotlin-dependencies.json`, which supplies both
the generated package manifests and the test installer. The Swift
transport fixture runs on Linux.

Generated-output tests compare complete results with golden files in
`testdata/golden`. To update expectations after an intentional output change:

```sh
make update-golden
git diff -- testdata/golden
```

Review the golden diff, then run `make test`.

`make update-golden` uses the same installed compilers and runtime dependencies
as `make test`. The complete typed-error SDK fixture compiles its generated
TypeScript, Kotlin, and Swift clients and types alongside the golden comparisons.
