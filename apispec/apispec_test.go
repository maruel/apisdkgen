// Tests for standalone API specification validation.

package apispec

import (
	"reflect"
	"testing"
)

type testResponse struct{}

func TestConfig(t *testing.T) {
	t.Parallel()
	t.Run("Validate", func(t *testing.T) {
		t.Parallel()
		t.Run("valid", func(t *testing.T) {
			t.Parallel()
			for _, tc := range []struct {
				name string
				cfg  Config[string]
			}{
				{"empty specification", Config[string]{}},
				{"root route without output configuration", Config[string]{Routes: []Route{{Name: "get", Method: "GET", Resp: reflect.TypeFor[testResponse]()}}}},
				{"default SSE method and payloadless event", Config[string]{Routes: []Route{{Name: "events", Path: "/events", IsSSE: true, Resp: reflect.TypeFor[testResponse](), SSEEvents: []SSEEvent{{Name: "ready", Handler: "onReady"}}}}}},
				{"output rules are deferred", Config[string]{KotlinPackage: "invalid..package", Routes: []Route{{Name: "events", IsSSE: true, Path: "/{onReady}", Resp: reflect.TypeFor[testResponse](), SSEEvents: []SSEEvent{{Name: "ready", Handler: "onReady"}}}}}},
				{"scoped method names are output dependent", Config[string]{ClientScopes: []ClientScope{{Name: "ws", PathPrefix: "/ws/{id}", Parameter: "id"}}, Routes: []Route{{Name: "get", Method: "GET", Path: "/event", Resp: reflect.TypeFor[testResponse]()}, {Name: "get", Method: "GET", Path: "/ws/{id}", Resp: reflect.TypeFor[testResponse]()}}}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					if err := tc.cfg.Validate(); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
		t.Run("error", func(t *testing.T) {
			t.Parallel()
			for _, tc := range []struct {
				name   string
				change func(*Config[string])
				want   string
			}{
				{"nil seed", func(c *Config[string]) { c.ExtraSeeds = []reflect.Type{nil} }, `ExtraSeeds[0]: type must not be nil`},
				{"nil special type", func(c *Config[string]) { c.SpecialTypes = []SpecialType{{}} }, `SpecialTypes[0]: type must not be nil`},
				{"invalid scope", func(c *Config[string]) { c.ClientScopes = []ClientScope{{Name: "bad-name"}} }, `ClientScopes[0]: invalid name "bad-name"`},
				{"missing scope parameter", func(c *Config[string]) {
					c.ClientScopes = []ClientScope{{Name: "ws", PathPrefix: "/ws", Parameter: "id"}}
				}, `ClientScopes[0]: parameter "id" must occur in PathPrefix`},
				{"missing response", func(c *Config[string]) { c.Routes[0].Resp = nil }, `routes[0] ("get"): response type is required`},
				{"malformed path", func(c *Config[string]) { c.Routes[0].Path = "/{id" }, `routes[0] ("get"): path "/{id" contains malformed parameters or URL delimiters`},
				{"duplicate parameter", func(c *Config[string]) { c.Routes[0].Path = "/{id}"; c.Routes[0].QueryParams = []string{"id"} }, `routes[0] ("get"): duplicate parameter "id"`},
				{"duplicate event", func(c *Config[string]) {
					c.Routes[0].IsSSE = true
					c.Routes[0].SSEEvents = []SSEEvent{{Name: "ready", Handler: "onReady"}, {Name: "ready", Handler: "onAgain"}}
				}, `routes[0] ("get"): SSEEvents[1]: event name "ready" is empty or duplicated`},
				{"duplicate handler", func(c *Config[string]) {
					c.Routes[0].IsSSE = true
					c.Routes[0].SSEEvents = []SSEEvent{{Name: "ready", Handler: "onReady"}, {Name: "reset", Handler: "onReady"}}
				}, `routes[0] ("get"): SSEEvents[1]: handler "onReady" is invalid or duplicated`},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					cfg := Config[string]{Routes: []Route{{Name: "get", Method: "GET", Path: "/event", Resp: reflect.TypeFor[testResponse]()}}}
					tc.change(&cfg)
					if err := cfg.Validate(); err == nil || err.Error() != tc.want {
						t.Fatalf("Validate() = %v; want %q", err, tc.want)
					}
				})
			}
		})
	})
}

func TestRoute(t *testing.T) {
	t.Parallel()
	t.Run("Validate", func(t *testing.T) {
		t.Parallel()
		t.Run("valid", func(t *testing.T) {
			t.Parallel()
			for _, tc := range []struct {
				name  string
				route Route
			}{
				{"root", Route{Name: "get", Method: "GET", Resp: reflect.TypeFor[testResponse]()}},
				{"keyword identifier", Route{Name: "type", Method: "GET", Path: "/{range}", QueryParams: []string{"default"}, Resp: reflect.TypeFor[testResponse]()}},
				{"SSE notification", Route{Name: "events", IsSSE: true, Path: "/events", Resp: reflect.TypeFor[testResponse](), SSEEvents: []SSEEvent{{Name: "ready", Handler: "onReady"}}}},
				{"generated callback names are deferred", Route{Name: "events", IsSSE: true, Path: "/events", Resp: reflect.TypeFor[testResponse](), SSEEvents: []SSEEvent{{Name: "ready", Handler: "onError"}}}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					if err := tc.route.Validate(); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
		t.Run("error", func(t *testing.T) {
			t.Parallel()
			for _, tc := range []struct {
				name   string
				change func(*Route)
				want   string
			}{
				{"missing response", func(r *Route) { r.Resp = nil }, "response type is required"},
				{"invalid name", func(r *Route) { r.Name = "1get" }, `invalid name "1get"`},
				{"missing method", func(r *Route) { r.Method = "" }, "HTTP method is required"},
				{"malformed path", func(r *Route) { r.Path = "/{id" }, `path "/{id" contains malformed parameters or URL delimiters`},
				{"duplicate parameter", func(r *Route) { r.Path = "/{id}"; r.QueryParams = []string{"id"} }, `duplicate parameter "id"`},
				{"events require SSE", func(r *Route) { r.SSEEvents = []SSEEvent{{Name: "ready", Handler: "onReady"}} }, "SSEEvents requires IsSSE"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					route := Route{Name: "get", Method: "GET", Path: "/event", Resp: reflect.TypeFor[testResponse]()}
					tc.change(&route)
					if err := route.Validate(); err == nil || err.Error() != tc.want {
						t.Fatalf("Validate() = %v; want %q", err, tc.want)
					}
				})
			}
		})
	})
}
