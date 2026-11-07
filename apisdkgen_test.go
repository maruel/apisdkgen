// Tests for public API generation validation.

package apisdkgen

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/maruel/apisdkgen/apispec"
)

type testNonScalarQuery struct {
	Values []string `query:"values"`
}

type testDuplicateQuery struct {
	First  string `query:"same"`
	Second string `query:"same"`
}

type testInvalidQueryName struct {
	Value string `query:"限度"`
}
type testDollarQueryName struct {
	Value string `query:"$filter"`
}

type testNativePropertyCollision struct {
	Limit int `json:"限度" query:"limit"`
	Other int `json:"limit" query:"other"`
}

func TestGenerateErrors(t *testing.T) {
	t.Parallel()
	t.Run("existing output survives invalid later route", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		path := filepath.Join(root, "api.gen.ts")
		const previous = "previous generated SDK\n"
		if err := os.WriteFile(path, []byte(previous), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg := apispec.Config[string]{Routes: []apispec.Route{
			{Name: "getEvent", Method: "GET", Path: "/event", Resp: reflect.TypeFor[testSDKEvent]()},
			{Name: "broken", Method: "GET", Path: "/broken"},
		}}
		api := NewAPI(root, "FixtureSDK", NewVersion("v1", ".", cfg))
		const want = `version v1: invalid API specification: routes[1] ("broken"): response type is required`
		if err := api.Generate(); err == nil || err.Error() != want {
			t.Fatalf("Generate() = %v; want %q", err, want)
		}
		data, err := fs.ReadFile(os.DirFS(root), "api.gen.ts")
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != previous {
			t.Fatalf("existing output = %q; want %q", data, previous)
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 {
			t.Fatalf("invalid input changed output directory: %v", entries)
		}
	})
	t.Run("constant preparation preserves output", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		source := filepath.Join(root, "types.go")
		if err := os.WriteFile(source, []byte("package fixture\ntype State string\nconst StateFirst State = external.Value\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		output := filepath.Join(root, "types.gen.ts")
		const previous = "previous SDK\n"
		if err := os.WriteFile(output, []byte(previous), 0o600); err != nil {
			t.Fatal(err)
		}
		api := NewAPI(root, "FixtureSDK", NewVersion("v1", root, apispec.Config[string]{}))
		api.Languages = []Language{TypeScript}
		if err := api.Generate(); err == nil || err.Error() != "version v1: loading docs: string constant StateFirst: unsupported expression *ast.SelectorExpr" {
			t.Fatalf("Generate() = %v", err)
		}
		data, err := fs.ReadFile(os.DirFS(root), "types.gen.ts")
		if err != nil || string(data) != previous {
			t.Fatalf("existing output = %q, %v", data, err)
		}
	})
	t.Run("query fields hidden from JSON", func(t *testing.T) {
		t.Parallel()
		type part struct {
			Token string `query:"token"`
		}
		type otherPart struct{ Token string }
		type deeper struct{ part }
		type repeated struct {
			Visible string `query:"visible"`
			part
			deeper
		}
		type ignored struct {
			Visible string `query:"visible"`
			Token   string `query:"token" json:"-"`
		}
		type private struct {
			Visible string `query:"visible"`
			token   string `query:"token"` //nolint:unused // Reflection-only field verifies hidden query metadata is rejected.
		}
		type ignoredEmbedding struct {
			Visible string `query:"visible"`
			part    `json:"-"`
		}
		type taggedEmbedding struct {
			part `query:"nested"`
		}
		type shadowed struct {
			Visible string `query:"visible"`
			part
			Token string
		}
		type ambiguous struct {
			Visible string `query:"visible"`
			part
			otherPart
		}
		for _, tc := range []struct {
			name  string
			query reflect.Type
			want  string
		}{
			{"ignored", reflect.TypeFor[ignored](), `apisdkgen.ignored.Token: query field "token" is not visible in JSON`},
			{"unexported", reflect.TypeFor[private](), `apisdkgen.private.token: query field "token" is not visible in JSON`},
			{"ignored embedding", reflect.TypeFor[ignoredEmbedding](), `apisdkgen.part.Token: query field "token" is not visible in JSON`},
			{"tagged embedding", reflect.TypeFor[taggedEmbedding](), `apisdkgen.taggedEmbedding.part: query field "nested" is not visible in JSON`},
			{"shadowed", reflect.TypeFor[shadowed](), `apisdkgen.part.Token: query field "token" is not visible in JSON`},
			{"ambiguous", reflect.TypeFor[ambiguous](), `apisdkgen.part.Token: query field "token" is not visible in JSON`},
			{"repeated owner", reflect.TypeFor[repeated](), `apisdkgen.part.Token: query field "token" is not visible in JSON`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				cfg := apispec.Config[string]{
					SDKPackagePaths: map[string]struct{}{tc.query.PkgPath(): {}},
					ExtraSeeds:      []reflect.Type{reflect.TypeFor[testSSEError]()},
					ErrorModel:      apispec.ClientErrorModel{TypeName: "testSSEError"},
					Routes:          []apispec.Route{{Name: "list", Method: "GET", Path: "/items", Query: tc.query, Resp: reflect.TypeFor[testSDKEvent]()}},
				}
				api := NewAPI(t.TempDir(), "FixtureSDK", NewVersion("v1", ".", cfg))
				api.Languages = []Language{TypeScript}
				want := `version v1: invalid API specification: routes[0] ("list"): ` + tc.want
				if err := api.Generate(); err == nil || err.Error() != want {
					t.Fatalf("Generate() = %v; want %q", err, want)
				}
			})
		}
	})

	t.Run("unseeded alias name", func(t *testing.T) {
		t.Parallel()
		source := t.TempDir()
		if err := os.WriteFile(filepath.Join(source, "types.go"), []byte("package fixture\ntype É string\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, lang := range []Language{Kotlin, Swift, TypeScript} {
			t.Run(string(lang), func(t *testing.T) {
				t.Parallel()
				cfg := apispec.Config[string]{
					KotlinPackage:   "fixture",
					SDKPackagePaths: map[string]struct{}{reflect.TypeFor[testSDKEvent]().PkgPath(): {}},
					ExtraSeeds:      []reflect.Type{reflect.TypeFor[testSSEError]()},
					ErrorModel:      apispec.ClientErrorModel{TypeName: "testSSEError"},
					Routes:          []apispec.Route{{Name: "list", Method: "GET", Path: "/items", Resp: reflect.TypeFor[testSDKEvent]()}},
				}
				api := NewAPI(filepath.Join(t.TempDir(), "sdk"), "FixtureSDK", NewVersion("v1", source, cfg))
				api.Languages = []Language{lang}
				const want = `version v1: invalid SDK types: invalid generated alias name "É"`
				if err := api.Generate(); err == nil || err.Error() != want {
					t.Fatalf("Generate() = %v; want %q", err, want)
				}
			})
		}
	})
	t.Run("native property collision", func(t *testing.T) {
		t.Parallel()
		for _, lang := range []Language{Kotlin, Swift} {
			t.Run(string(lang), func(t *testing.T) {
				t.Parallel()
				cfg := apispec.Config[string]{
					KotlinPackage:   "fixture",
					SDKPackagePaths: map[string]struct{}{reflect.TypeFor[testSDKEvent]().PkgPath(): {}},
					ExtraSeeds:      []reflect.Type{reflect.TypeFor[testSSEError]()},
					ErrorModel:      apispec.ClientErrorModel{TypeName: "testSSEError"},
					Routes:          []apispec.Route{{Name: "list", Method: "GET", Path: "/items", Query: reflect.TypeFor[testNativePropertyCollision](), Resp: reflect.TypeFor[testSDKEvent]()}},
				}
				api := NewAPI(filepath.Join(t.TempDir(), "sdk"), "FixtureSDK", NewVersion("v1", ".", cfg))
				api.Languages = []Language{lang}
				want := `version v1: duplicate generated ` + string(lang) + ` property name "limit" in apisdkgen.testNativePropertyCollision`
				if err := api.Generate(); err == nil || err.Error() != want {
					t.Fatalf("Generate() = %v; want %q", err, want)
				}
			})
		}
	})

	t.Run("invalid specification", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name   string
			change func(*apispec.Config[string])
			want   string
		}{
			{"scope method collision", func(c *apispec.Config[string]) {
				c.ClientScopes = []apispec.ClientScope{{Name: "getEvent", PathPrefix: "/ws/{wsID}", Parameter: "wsID"}}
			}, `routes[0] ("getEvent"): duplicate route name in client scope ""`},
			{"MCP method collision", func(c *apispec.Config[string]) { c.MCPProtocolVersion = "test"; c.Routes[0].Name = "serverDiscover" }, `routes[0] ("serverDiscover"): duplicate route name in client scope ""`},
			{"SSE handler type collision", func(c *apispec.Config[string]) {
				c.Routes[0].IsSSE = true
				other := c.Routes[0]
				other.Name = "GetEvent"
				c.Routes = append(c.Routes, other)
			}, `routes[1] ("GetEvent"): duplicate SSE handler type "GetEventHandlers"`},
			{"native reconnect method collision", func(c *apispec.Config[string]) {
				c.Routes[0].IsSSE = true
				other := c.Routes[0]
				other.IsSSE = false
				other.Name = "getEventReconnecting"
				c.Routes = append(c.Routes, other)
			}, `routes[1] ("getEventReconnecting"): duplicate native method "getEventReconnecting"`},
			{"native SSE callback collision", func(c *apispec.Config[string]) {
				c.Routes[0].IsSSE = true
				c.Routes[0].Path = "/{id}"
				c.Routes[0].SSEEvents = []apispec.SSEEvent{{Name: "ready", Handler: "id"}}
			}, `routes[0] ("getEvent"): SSE handler "id" conflicts with a route parameter`},
			{"native SSE reserved callback", func(c *apispec.Config[string]) {
				c.Routes[0].IsSSE = true
				c.Routes[0].SSEEvents = []apispec.SSEEvent{{Name: "ready", Handler: "onEventID"}}
			}, `routes[0] ("getEvent"): SSE handler "onEventID" conflicts with a generated argument`},
			{"native SSE cursor collision", func(c *apispec.Config[string]) {
				c.Routes[0].IsSSE = true
				c.Routes[0].QueryParams = []string{"lastEventID"}
				c.Routes[0].SSEEvents = []apispec.SSEEvent{{Name: "ready", Handler: "onReady"}}
			}, `routes[0] ("getEvent"): parameter "lastEventID" conflicts with a generated argument`},
			{"duplicate scope", func(c *apispec.Config[string]) {
				scope := apispec.ClientScope{Name: "ws", PathPrefix: "/ws/{wsID}", Parameter: "wsID"}
				c.ClientScopes = []apispec.ClientScope{scope, scope}
			}, `ClientScopes[1]: duplicate name "ws"`},
			{"malformed scope path", func(c *apispec.Config[string]) {
				c.ClientScopes = []apispec.ClientScope{{Name: "ws", PathPrefix: "/ws/{", Parameter: "wsID"}}
			}, `ClientScopes[0]: path "/ws/{" contains malformed parameters or URL delimiters`},
			{"specification before output rules", func(c *apispec.Config[string]) { c.KotlinPackage = ""; c.Routes[0].Resp = nil }, `routes[0] ("getEvent"): response type is required`},
			{"missing Kotlin package", func(c *apispec.Config[string]) { c.KotlinPackage = "" }, `KotlinPackage "" must be a nonempty dotted package name`},
			{"malformed Kotlin package", func(c *apispec.Config[string]) { c.KotlinPackage = "api..sdk" }, `KotlinPackage "api..sdk" must be a nonempty dotted package name`},
			{"nil seed", func(c *apispec.Config[string]) { c.ExtraSeeds = []reflect.Type{nil} }, `ExtraSeeds[0]: type must not be nil`},
			{"nil special type", func(c *apispec.Config[string]) { c.SpecialTypes = []apispec.SpecialType{{}} }, `SpecialTypes[0]: type must not be nil`},
			{"empty route name", func(c *apispec.Config[string]) { c.Routes[0].Name = "" }, `routes[0] (""): invalid name ""`},
			{"invalid route name", func(c *apispec.Config[string]) { c.Routes[0].Name = "get-event" }, `routes[0] ("get-event"): invalid name "get-event"`},
			{"missing method", func(c *apispec.Config[string]) { c.Routes[0].Method = "" }, `routes[0] ("getEvent"): HTTP method is required`},
			{"invalid method", func(c *apispec.Config[string]) { c.Routes[0].Method = "get" }, `routes[0] ("getEvent"): unsupported HTTP method "get"`},
			{"relative path", func(c *apispec.Config[string]) { c.Routes[0].Path = "event" }, `routes[0] ("getEvent"): path "event" must start with /`},
			{"unclosed parameter", func(c *apispec.Config[string]) { c.Routes[0].Path = "/event/{id" }, `routes[0] ("getEvent"): path "/event/{id" contains malformed parameters or URL delimiters`},
			{"invalid path parameter", func(c *apispec.Config[string]) { c.Routes[0].Path = "/{1id}" }, `routes[0] ("getEvent"): path "/{1id}" has invalid parameter "1id"`},
			{"duplicate path parameter", func(c *apispec.Config[string]) { c.Routes[0].Path = "/{id}/{id}" }, `routes[0] ("getEvent"): path "/{id}/{id}" repeats parameter "id"`},
			{"missing response", func(c *apispec.Config[string]) { c.Routes[0].Resp = nil }, `routes[0] ("getEvent"): response type is required`},
			{"unnamed response", func(c *apispec.Config[string]) { c.Routes[0].Resp = reflect.TypeFor[struct{}]() }, `routes[0] ("getEvent"): response type struct {} must be named`},
			{"pointer request", func(c *apispec.Config[string]) { c.Routes[0].Req = reflect.TypeFor[*testSDKEvent]() }, `routes[0] ("getEvent"): request type *apisdkgen.testSDKEvent must be named`},
			{"duplicate route", func(c *apispec.Config[string]) { c.Routes = append(c.Routes, c.Routes[0]) }, `routes[1] ("getEvent"): duplicate route name in client scope ""`},
			{"invalid query parameter", func(c *apispec.Config[string]) { c.Routes[0].QueryParams = []string{"page-size"} }, `routes[0] ("getEvent"): invalid query parameter "page-size"`},
			{"duplicate query parameter", func(c *apispec.Config[string]) { c.Routes[0].QueryParams = []string{"page", "page"} }, `routes[0] ("getEvent"): duplicate parameter "page"`},
			{"argument collision", func(c *apispec.Config[string]) { c.Routes[0].Path = "/{headers}"; c.Routes[0].HeadersArg = true }, `routes[0] ("getEvent"): parameter "headers" conflicts with a generated argument`},
			{"nonstruct query", func(c *apispec.Config[string]) { c.Routes[0].Query = reflect.TypeFor[testGeneratedErrorCode]() }, `routes[0] ("getEvent"): query type must be a struct`},
			{"no query fields", func(c *apispec.Config[string]) { c.Routes[0].Query = reflect.TypeFor[testSDKEvent]() }, `routes[0] ("getEvent"): query type apisdkgen.testSDKEvent has no query-tagged JSON fields`},
			{"mixed query modes", func(c *apispec.Config[string]) {
				c.Routes[0].Query = reflect.TypeFor[testEmbeddedQueryRequest]()
				c.Routes[0].QueryParams = []string{"cursor"}
			}, `routes[0] ("getEvent"): typed query cannot be combined with QueryParams`},
			{"nonidentifier query name", func(c *apispec.Config[string]) { c.Routes[0].Query = reflect.TypeFor[testDollarQueryName]() }, `routes[0] ("getEvent"): invalid or duplicate query field "$filter"`},
			{"nonASCII query name", func(c *apispec.Config[string]) { c.Routes[0].Query = reflect.TypeFor[testInvalidQueryName]() }, `routes[0] ("getEvent"): invalid or duplicate query field "限度"`},
			{"nonASCII route name", func(c *apispec.Config[string]) { c.Routes[0].Name = "líst" }, `routes[0] ("líst"): invalid name "líst"`},
			{"nonscalar query field", func(c *apispec.Config[string]) { c.Routes[0].Query = reflect.TypeFor[testNonScalarQuery]() }, `routes[0] ("getEvent"): query field "values" has nonscalar type []string`},
			{"duplicate query field", func(c *apispec.Config[string]) { c.Routes[0].Query = reflect.TypeFor[testDuplicateQuery]() }, `routes[0] ("getEvent"): invalid or duplicate query field "same"`},
			{"query argument collision", func(c *apispec.Config[string]) {
				c.Routes[0].Query = reflect.TypeFor[testEmbeddedQueryRequest]()
				c.Routes[0].Path = "/{query}"
			}, `routes[0] ("getEvent"): parameter "query" conflicts with a generated argument`},
			{"query helper collision", func(c *apispec.Config[string]) {
				c.Routes[0].Query = reflect.TypeFor[testEmbeddedQueryFields]()
				c.Routes[0].Name = "queryPath"
			}, `routes[0] ("queryPath"): duplicate native method "queryPath"`},
			{"nonscalar query mapping", func(c *apispec.Config[string]) {
				c.Routes[0].Query = reflect.TypeFor[testNonScalarQuery]()
				c.SpecialTypes = []apispec.SpecialType{{Type: reflect.TypeFor[[]string](), TSType: "string[]", KTType: "List<String>", SwiftType: "[String]"}}
			}, `routes[0] ("getEvent"): query field "values" must map to a scalar in each selected language`},
			{"non GET SSE", func(c *apispec.Config[string]) { c.Routes[0].IsSSE = true; c.Routes[0].Method = "POST" }, `routes[0] ("getEvent"): SSE method must be GET`},
			{"events without SSE", func(c *apispec.Config[string]) {
				c.Routes[0].SSEEvents = []apispec.SSEEvent{{Name: "ready", Handler: "onReady"}}
			}, `routes[0] ("getEvent"): SSEEvents requires IsSSE`},
			{"empty event", func(c *apispec.Config[string]) {
				c.Routes[0].IsSSE = true
				c.Routes[0].SSEEvents = []apispec.SSEEvent{{Handler: "onReady"}}
			}, `routes[0] ("getEvent"): SSEEvents[0]: event name "" is empty or duplicated`},
			{"reserved handler", func(c *apispec.Config[string]) {
				c.Routes[0].IsSSE = true
				c.Routes[0].SSEEvents = []apispec.SSEEvent{{Name: "ready", Handler: "onError"}}
			}, `routes[0] ("getEvent"): SSEEvents[0]: handler "onError" is invalid or duplicated`},
			{"unnamed event response", func(c *apispec.Config[string]) {
				c.Routes[0].IsSSE = true
				c.Routes[0].SSEEvents = []apispec.SSEEvent{{Name: "ready", Handler: "onReady", Resp: reflect.TypeFor[struct{}]()}}
			}, `routes[0] ("getEvent"): SSEEvents[0]: response type struct {} must be named`},
			{"invalid scope", func(c *apispec.Config[string]) { c.ClientScopes = []apispec.ClientScope{{Name: "bad-name"}} }, `ClientScopes[0]: invalid name "bad-name"`},
			{"missing scope parameter", func(c *apispec.Config[string]) {
				c.ClientScopes = []apispec.ClientScope{{Name: "workspace", PathPrefix: "/workspaces", Parameter: "wsID"}}
			}, `ClientScopes[0]: parameter "wsID" must occur in PathPrefix`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				cfg := apispec.Config[string]{KotlinPackage: "fixture.api", Routes: []apispec.Route{{Name: "getEvent", Method: "GET", Path: "/event", Resp: reflect.TypeFor[testSDKEvent]()}}}
				tc.change(&cfg)
				root := t.TempDir()
				// An absent source directory also proves validation precedes documentation loading.
				api := NewAPI(filepath.Join(root, "sdk"), "FixtureSDK", NewVersion("v1", filepath.Join(root, "source"), cfg))
				want := "version v1: invalid API specification: " + tc.want
				if err := api.Generate(); err == nil || err.Error() != want {
					t.Fatalf("Generate() = %v; want %q", err, want)
				}
				entries, err := os.ReadDir(root)
				if err != nil {
					t.Fatal(err)
				}
				if len(entries) != 0 {
					t.Fatalf("invalid input created files: %v", entries)
				}
			})
		}
	})
}

func TestOutputPaths(t *testing.T) {
	t.Parallel()
	t.Run("validate", func(t *testing.T) {
		t.Parallel()
		t.Run("valid", func(t *testing.T) {
			t.Parallel()
			for _, tc := range []struct {
				name   string
				output outputPaths
				cfg    apispec.Config[string]
			}{
				{"TypeScript without Kotlin package", outputPaths{TypeScriptDir: "unused"}, apispec.Config[string]{}},
				{"Kotlin package", outputPaths{KotlinDir: "unused"}, apispec.Config[string]{KotlinPackage: "fixture.api"}},
				{"other outputs ignore Kotlin package", outputPaths{SwiftDir: "unused", MarkdownFile: "unused"}, apispec.Config[string]{KotlinPackage: "invalid..package"}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					if err := tc.cfg.Validate(); err != nil {
						t.Fatal(err)
					}
					if err := tc.output.validate(&tc.cfg); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
		t.Run("scoped names", testValidateScopedNames)
	})
}

func testValidateScopedNames(t *testing.T) {
	t.Parallel()
	cfg := apispec.Config[string]{
		KotlinPackage: "fixture.api",
		ClientScopes:  []apispec.ClientScope{{Name: "a", PathPrefix: "/a/{id}", Parameter: "id"}, {Name: "b", PathPrefix: "/b/{id}", Parameter: "id"}},
		Routes: []apispec.Route{
			{Name: "events", Method: "GET", Path: "/a/{id}/events", Resp: reflect.TypeFor[testSDKEvent]()},
			{Name: "events", Method: "GET", Path: "/b/{id}/events", Resp: reflect.TypeFor[testSDKEvent]()},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	t.Run("valid", func(t *testing.T) {
		output := outputPaths{TypeScriptDir: "unused"}
		if err := output.validate(&cfg); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("error", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			output outputPaths
		}{
			{"Kotlin", outputPaths{KotlinDir: "unused"}},
			{"Swift", outputPaths{SwiftDir: "unused"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				const want = `routes[1] ("events"): duplicate native method "events"`
				if err := tc.output.validate(&cfg); err == nil || err.Error() != want {
					t.Fatalf("got %v; want %q", err, want)
				}
			})
		}
		t.Run("module wide SSE handlers", func(t *testing.T) {
			cfg := cfg
			cfg.Routes = slices.Clone(cfg.Routes)
			for i := range cfg.Routes {
				cfg.Routes[i].IsSSE = true
			}
			const want = `routes[1] ("events"): duplicate SSE handler type "EventsHandlers"`
			output := outputPaths{TypeScriptDir: "unused"}
			if err := output.validate(&cfg); err == nil || err.Error() != want {
				t.Fatalf("got %v; want %q", err, want)
			}
		})
	})
}

func TestVerify(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "sdk")
	cfg := apispec.Config[string]{Routes: []apispec.Route{{Name: "getEvent", Method: "GET", Path: "/event", Resp: reflect.TypeFor[testSDKEvent]()}}}
	api := NewAPI(out, "FixtureSDK", NewVersion("v1", ".", cfg))
	api.Languages = []Language{TypeScript}
	if err := api.Generate(); err != nil {
		t.Fatal(err)
	}
	if err := api.Verify(); err != nil {
		t.Fatal(err)
	}
	changed := filepath.Join(out, "ts", "v1", "api.gen.ts")
	missing := filepath.Join(out, "ts", "v1", "types.gen.ts")
	obsolete := filepath.Join(out, "old.ts")
	if err := os.WriteFile(changed, []byte("user edit"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(obsolete, []byte("no generated header"), 0o600); err != nil {
		t.Fatal(err)
	}
	staleDir := filepath.Join(out, "old-package")
	if err := os.Mkdir(staleDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staleDir, "old.ts"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(root, "external")
	if err := os.Mkdir(external, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, "keep"), []byte("outside output"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(out, "linked")
	if err := os.Symlink(external, link); err != nil {
		t.Fatal(err)
	}
	want := "SDK output is stale:\nchanged: " + changed + "\nmissing: " + missing + "\nobsolete: " + link + "\nobsolete: " + staleDir + "\nobsolete: " + obsolete
	if err := api.Verify(); err == nil || err.Error() != want {
		t.Fatalf("Verify() = %v; want %s", err, want)
	}
	//nolint:gosec // The path is inside this test’s temporary directory.
	if data, err := os.ReadFile(changed); err != nil || string(data) != "user edit" {
		t.Fatalf("changed output = %q, %v", data, err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("missing output: %v", err)
	}
	//nolint:gosec // The path is inside this test’s temporary directory.
	if data, err := os.ReadFile(obsolete); err != nil || string(data) != "no generated header" {
		t.Fatalf("obsolete output = %q, %v", data, err)
	}
	bad := NewAPI(out, "FixtureSDK", NewVersion("v1", filepath.Join(root, "absent"), cfg))
	bad.Languages = []Language{TypeScript}
	if err := bad.Verify(); err == nil {
		t.Fatal("expected generation failure")
	}
	//nolint:gosec // The path is inside this test’s temporary directory.
	if data, err := os.ReadFile(changed); err != nil || string(data) != "user edit" {
		t.Fatalf("after failure = %q, %v", data, err)
	}
	if err := os.Remove(changed); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(changed, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := api.Verify(); err == nil {
		t.Fatal("expected comparison failure")
	}
	if info, err := os.Stat(changed); err != nil || !info.IsDir() {
		t.Fatalf("comparison changed directory: %v", err)
	}
	if err := api.Generate(); err != nil {
		t.Fatal(err)
	}
	if err := api.Verify(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, staleDir, obsolete} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("stale entry %s survived generation: %v", path, err)
		}
	}
	//nolint:gosec // The path is inside this test's temporary directory.
	if data, err := os.ReadFile(filepath.Join(external, "keep")); err != nil || string(data) != "outside output" {
		t.Fatalf("external file = %q, %v", data, err)
	}
}

func TestAPI(t *testing.T) {
	t.Parallel()
	first := NewVersion("v1", ".", apispec.Config[string]{
		KotlinPackage: "fixture.v1", APIDocTitle: "First API",
		SDKPackagePaths: map[string]struct{}{reflect.TypeFor[testSSEError]().PkgPath(): {}},
		ExtraSeeds:      []reflect.Type{reflect.TypeFor[testSSEError]()},
		ErrorModel:      apispec.ClientErrorModel{TypeName: "testSSEError", TSCodeExpr: `"UNKNOWN"`, TSDetailsExpr: "undefined", KTCodeExpr: `"UNKNOWN"`, KTMessageExpr: "err.message", KTDetailsExpr: "null", SwiftCodeExpr: `"UNKNOWN"`, SwiftMessageExpr: "errResp.message", SwiftDetailsExpr: "nil"},
	})
	cfg := discoveryConfig(reflect.TypeFor[testSDKEvent]())
	cfg.KotlinPackage = "fixture.v2"
	cfg.APIDocTitle = "Second API"
	second := NewVersion("v2", ".", cfg)
	t.Run("Generate", func(t *testing.T) {
		t.Parallel()
		root := filepath.Join(t.TempDir(), "sdk")
		api := NewAPI(root, "FixtureSDK", second, first)
		if err := api.Generate(); err != nil {
			t.Fatal(err)
		}
		if err := api.Verify(); err != nil {
			t.Fatal(err)
		}
		files := discoveryFiles(t, root)
		names := make([]string, 0, len(files))
		for path := range files {
			names = append(names, path)
		}
		slices.Sort(names)
		want := []string{
			"API.v1.md", "API.v2.md",
			"kotlin/.gitignore", "kotlin/build.gradle.kts", "kotlin/settings.gradle.kts",
			"kotlin/src/main/kotlin/fixture/v1/ApiClient.kt", "kotlin/src/main/kotlin/fixture/v1/Types.kt",
			"kotlin/src/main/kotlin/fixture/v2/ApiClient.kt", "kotlin/src/main/kotlin/fixture/v2/Types.kt",
			"swift/.gitignore", "swift/Package.swift",
			"swift/Sources/FixtureSDKV1/ApiClient.swift", "swift/Sources/FixtureSDKV1/Types.swift",
			"swift/Sources/FixtureSDKV2/ApiClient.swift", "swift/Sources/FixtureSDKV2/Types.swift",
			"ts/.gitignore", "ts/package.json", "ts/tsconfig.json",
			"ts/v1/api.gen.ts", "ts/v1/types.gen.ts", "ts/v2/api.gen.ts", "ts/v2/types.gen.ts",
		}
		if !slices.Equal(names, want) {
			t.Fatalf("package files = %v; want %v", names, want)
		}
		for _, path := range []string{"kotlin/build.gradle.kts", "kotlin/settings.gradle.kts", "swift/Package.swift", "ts/package.json", "ts/tsconfig.json"} {
			assertGolden(t, filepath.Base(path), files[path])
		}
		if files["API.v1.md"] == files["API.v2.md"] {
			t.Fatal("versions share documentation")
		}
		reversed := NewAPI(root, "FixtureSDK", first, second)
		if err := reversed.Generate(); err != nil {
			t.Fatal(err)
		}
		if got := discoveryFiles(t, root); !reflect.DeepEqual(files, got) {
			t.Fatal("version registration order changed generated output")
		}
		t.Run("TypeScript package", func(t *testing.T) {
			consumer := filepath.Join(root, "ts", "v1", "consumer.ts")
			if err := os.WriteFile(consumer, []byte(`import { createApiClient as v1 } from "fixturesdk/v1/api.gen";
import { createApiClient as v2 } from "fixturesdk/v2/api.gen";
export const clients = [v1(), v2()];
`), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.Remove(consumer); err != nil {
					t.Error(err)
				}
			})
			// #nosec G204 G702 -- The compiler and flags are fixed; the project is this test's generated temporary package.
			cmd := exec.CommandContext(t.Context(), "tsc", "--project", filepath.Join(root, "ts"))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("TypeScript package: %v\n%s", err, out)
			}
		})
		t.Run("Swift manifest", func(t *testing.T) {
			if _, err := exec.LookPath("swift"); err != nil {
				t.Skip("run make test to configure Swift")
			}
			// #nosec G204 G702 -- Swift parses this test's generated temporary package without a shell.
			cmd := exec.CommandContext(t.Context(), "swift", "package", "--package-path", filepath.Join(root, "swift"), "dump-package")
			data, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			var pkg struct {
				Name     string
				Targets  []struct{ Name string }
				Products []struct{ Name string }
			}
			if err := json.Unmarshal(data, &pkg); err != nil {
				t.Fatal(err)
			}
			if pkg.Name != "FixtureSDK" || len(pkg.Targets) != 2 || pkg.Targets[0].Name != "FixtureSDKV1" || pkg.Targets[1].Name != "FixtureSDKV2" || len(pkg.Products) != 2 || pkg.Products[0].Name != "FixtureSDKV1" || pkg.Products[1].Name != "FixtureSDKV2" {
				t.Fatalf("Swift package = %+v", pkg)
			}
		})
		// Building a package may populate these standard tool caches.
		for _, path := range []string{"kotlin/.gradle", "kotlin/.kotlin", "kotlin/build", "swift/.build", "swift/.swiftpm", "ts/node_modules"} {
			dir := filepath.Join(root, filepath.FromSlash(path))
			if err := os.MkdirAll(dir, 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "cache"), []byte("tool output"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := api.Verify(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "unexpected"), []byte("old output"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := api.Verify(); err == nil || err.Error() != "SDK output is stale:\nobsolete: "+filepath.Join(root, "unexpected") {
			t.Fatalf("unexpected file = %v", err)
		}
		if err := api.Generate(); err != nil {
			t.Fatal(err)
		}
		if got := discoveryFiles(t, root); !reflect.DeepEqual(files, got) {
			t.Fatal("generation did not replace entire package")
		}
	})
	t.Run("output directory symlinks", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct{ name, suffix string }{
			{name: "clean"},
			{name: "trailing separator", suffix: string(os.PathSeparator)},
			{name: "trailing dot segment", suffix: string(os.PathSeparator) + "." + string(os.PathSeparator)},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				base := t.TempDir()
				external := filepath.Join(base, "external")
				original := NewAPI(external, "FixtureSDK", first)
				original.Languages = []Language{TypeScript}
				if err := original.Generate(); err != nil {
					t.Fatal(err)
				}
				files := discoveryFiles(t, external)
				root := filepath.Join(base, "sdk")
				if err := os.Symlink(external, root); err != nil {
					t.Fatal(err)
				}
				api := NewAPI(root+tc.suffix, "FixtureSDK", first)
				api.Languages = []Language{TypeScript}
				if err := api.Verify(); err == nil || err.Error() != "SDK output is stale:\nchanged: "+root {
					t.Fatalf("root symlink Verify = %v", err)
				}
				if err := api.Generate(); err != nil {
					t.Fatal(err)
				}
				if info, err := os.Lstat(root); err != nil || !info.IsDir() {
					t.Fatalf("generated root = %v, %v", info, err)
				}
				if got := discoveryFiles(t, external); !reflect.DeepEqual(files, got) {
					t.Fatal("generation changed external root")
				}
				child := filepath.Join(root, "ts")
				moved := filepath.Join(base, "external-ts")
				if err := os.Rename(child, moved); err != nil {
					t.Fatal(err)
				}
				subtree := discoveryFiles(t, moved)
				if err := os.Symlink(moved, child); err != nil {
					t.Fatal(err)
				}
				if err := api.Verify(); err == nil || err.Error() != "SDK output is stale:\nchanged: "+child {
					t.Fatalf("expected directory symlink Verify = %v", err)
				}
				if err := api.Generate(); err != nil {
					t.Fatal(err)
				}
				if got := discoveryFiles(t, moved); !reflect.DeepEqual(subtree, got) {
					t.Fatal("generation changed external subtree")
				}
				if err := api.Verify(); err != nil {
					t.Fatal(err)
				}
			})
		}
	})

	t.Run("invalid output roots", func(t *testing.T) {
		t.Parallel()
		for _, root := range []string{"", ".", "." + string(os.PathSeparator), "nested" + string(os.PathSeparator) + "..", string(os.PathSeparator)} {
			// No versions keeps this test non-destructive if root validation regresses.
			api := NewAPI(root, "FixtureSDK")
			if err := api.Generate(); err == nil || err.Error() != "output directory must be a non-root package directory" {
				t.Errorf("Generate(%q) = %v", root, err)
			}
		}
	})

	t.Run("invalid input preserves output", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name      string
			versions  []Version
			languages []Language
			want      string
		}{
			{name: "no versions", want: "at least one API version is required"},
			{name: "duplicate version", versions: []Version{first, first}, want: `duplicate API version "v1"`},
			{name: "invalid version", versions: []Version{NewVersion("../v1", ".", apispec.Config[string]{})}, want: `invalid API version "../v1"`},
			{name: "zero version", versions: []Version{NewVersion("v0", ".", apispec.Config[string]{})}, want: `invalid API version "v0"`},
			{name: "unconstructed version", versions: []Version{{}}, want: `invalid API version ""`},
			{name: "duplicate Kotlin namespace", versions: []Version{first, NewVersion("v2", ".", apispec.Config[string]{KotlinPackage: "fixture.v1"})}, want: `duplicate Kotlin package "fixture.v1"`},
			{name: "unknown language", versions: []Version{first}, languages: []Language{"Rust"}, want: `unknown language "Rust"`},
			{name: "duplicate language", versions: []Version{first}, languages: []Language{TypeScript, TypeScript}, want: `duplicate language "TypeScript"`},
			{name: "invalid later specification", versions: []Version{first, NewVersion("v2", ".", apispec.Config[string]{Routes: []apispec.Route{{Name: "broken", Method: "GET", Path: "/"}}})}, want: `version v2: invalid API specification: routes[0] ("broken"): response type is required`},
			{name: "invalid later namespace", versions: []Version{first, NewVersion("v2", ".", apispec.Config[string]{KotlinPackage: "../escape"})}, want: `version v2: invalid API specification: KotlinPackage "../escape" must be a nonempty dotted package name`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				if err := os.WriteFile(filepath.Join(root, "previous"), []byte("existing output"), 0o600); err != nil {
					t.Fatal(err)
				}
				api := NewAPI(root, "FixtureSDK", tc.versions...)
				api.Languages = tc.languages
				if err := api.Generate(); err == nil || err.Error() != tc.want {
					t.Fatalf("Generate = %v; want %s", err, tc.want)
				}
				if got := discoveryFiles(t, root); !reflect.DeepEqual(got, map[string]string{"previous": "existing output"}) {
					t.Fatalf("invalid input changed output: %v", got)
				}
			})
		}
	})
}
