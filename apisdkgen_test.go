// Tests for public API generation validation.

package apisdkgen

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/maruel/apisdkgen/apispec"
)

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
		api := NewAPI(".", OutputConfig{TypeScriptDir: root}, cfg)
		const want = `invalid API specification: routes[1] ("broken"): response type is required`
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
			{"missing query request", func(c *apispec.Config[string]) { c.Routes[0].QueryFromReq = true }, `routes[0] ("getEvent"): query request must be a struct`},
			{"missing query field", func(c *apispec.Config[string]) {
				c.Routes[0].QueryFromReq = true
				c.Routes[0].Req = reflect.TypeFor[testSDKEvent]()
				c.Routes[0].QueryParams = []string{"cursor"}
			}, `routes[0] ("getEvent"): missing query field "cursor"`},
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
				output := OutputConfig{TypeScriptDir: filepath.Join(root, "ts"), KotlinDir: filepath.Join(root, "kt"), SwiftDir: filepath.Join(root, "swift"), MarkdownDir: filepath.Join(root, "docs")}
				// An absent source directory also proves validation precedes documentation loading.
				api := NewAPI(filepath.Join(root, "source"), output, cfg)
				want := "invalid API specification: " + tc.want
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

func TestOutputConfig(t *testing.T) {
	t.Parallel()
	t.Run("validate", func(t *testing.T) {
		t.Parallel()
		t.Run("valid", func(t *testing.T) {
			t.Parallel()
			for _, tc := range []struct {
				name   string
				output OutputConfig
				cfg    apispec.Config[string]
			}{
				{"TypeScript without Kotlin package", OutputConfig{TypeScriptDir: "unused"}, apispec.Config[string]{}},
				{"Kotlin package", OutputConfig{KotlinDir: "unused"}, apispec.Config[string]{KotlinPackage: "fixture.api"}},
				{"other outputs ignore Kotlin package", OutputConfig{SwiftDir: "unused", MarkdownDir: "unused"}, apispec.Config[string]{KotlinPackage: "invalid..package"}},
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
		output := OutputConfig{TypeScriptDir: "unused"}
		if err := output.validate(&cfg); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("error", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			output OutputConfig
		}{
			{"Kotlin", OutputConfig{KotlinDir: "unused"}},
			{"Swift", OutputConfig{SwiftDir: "unused"}},
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
			output := OutputConfig{TypeScriptDir: "unused"}
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
	api := NewAPI(".", OutputConfig{TypeScriptDir: out, MarkdownDir: out}, cfg)
	if err := api.Generate(); err != nil {
		t.Fatal(err)
	}
	if err := api.Verify(); err != nil {
		t.Fatal(err)
	}
	changed := filepath.Join(out, "api.gen.ts")
	missing := filepath.Join(out, "types.gen.ts")
	obsolete := filepath.Join(out, "old.ts")
	validator := filepath.Join(out, "validate.gen.ts")
	if err := os.WriteFile(validator, []byte("previous validator"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(changed, []byte("user edit"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(obsolete, []byte("// Code generated by gen-api-sdk. DO NOT EDIT.\nold"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "handwritten.ts"), []byte("handwritten"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := "SDK output is stale:\nchanged: " + changed + "\nmissing: " + missing + "\nobsolete: " + obsolete + "\nobsolete: " + validator
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
	if data, err := os.ReadFile(obsolete); err != nil || string(data) != "// Code generated by gen-api-sdk. DO NOT EDIT.\nold" {
		t.Fatalf("obsolete output = %q, %v", data, err)
	}
	bad := NewAPI(filepath.Join(root, "absent"), OutputConfig{TypeScriptDir: out}, cfg)
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
}
