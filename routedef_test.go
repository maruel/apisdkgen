// Tests for SDK route method generation and scoped URL paths.
package apisdkgen

import (
	"reflect"
	"strings"
	"testing"

	"github.com/maruel/apisdkgen/apispec"
)

type testEmbeddedQueryFields struct {
	Limit int `json:"limit" query:"limit"`
}

type testEmbeddedQueryRequest struct {
	testEmbeddedQueryFields
}

func TestWriteRouteTSJSONMethod(t *testing.T) {
	t.Parallel()
	type query struct {
		Limit int  `json:"limit" query:"limit"`
		Only  bool `json:"unread_only" query:"unread_only"`
	}
	t.Run("valid", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			route  apispec.Route
			params []string
		}{
			{"scoped query", apispec.Route{
				Name: "list", Method: "GET", Path: "/api/v1/workspaces/{wsID}/items/{id}",
				Query: reflect.TypeFor[query](), Resp: reflect.TypeFor[query](),
			}, []string{"id"}},
			{"embedded query", apispec.Route{
				Name: "list", Method: "GET", Path: "/items",
				Query: reflect.TypeFor[testEmbeddedQueryRequest](), Resp: reflect.TypeFor[testEmbeddedQueryRequest](),
			}, nil},
		} {
			t.Run(tc.name, func(t *testing.T) {
				d := &docRegistry[string]{cfg: &apispec.Config[string]{SDKPackagePaths: map[string]struct{}{reflect.TypeFor[testEmbeddedQueryRequest]().PkgPath(): {}}}}
				var b strings.Builder
				fields, err := routeQueryFields(d, &tc.route)
				if err != nil {
					t.Fatal(err)
				}
				writeRouteTSJSONMethod(&tc.route, &b, tc.params, fields)
				assertGolden(t, "method.ts", b.String())
			})
		}
	})
}

func TestRouteClientScope(t *testing.T) {
	t.Parallel()
	scopes := []apispec.ClientScope{{Name: "ws", PathPrefix: "/api/v1/workspaces/{wsID}", Parameter: "wsID"}}
	for _, tc := range []struct {
		name string
		path string
		want *apispec.ClientScope
	}{
		{"descendant", "/api/v1/workspaces/{wsID}/items", &scopes[0]},
		{"prefix boundary", "/api/v1/workspaces/{wsID}extra", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := routeClientScope(scopes, tc.path); got != tc.want {
				t.Fatalf("routeClientScope() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWriteRouteTSSSEMethod(t *testing.T) {
	t.Parallel()
	r := apispec.Route{Name: "events", Path: "/items/{id}/events", Resp: reflect.TypeFor[testSDKEvent](), IsSSE: true, QueryParams: []string{"cursor", "filter"}}
	var b strings.Builder
	writeRouteTSSSEMethod(&r, &b, []string{"id"}, nil)
	assertGolden(t, "method.ts", b.String())
}

func TestWriteRouteKotlinSSEFunc(t *testing.T) {
	t.Parallel()
	r := apispec.Route{Name: "events", Path: "/items/{id}/events", Resp: reflect.TypeFor[testSDKEvent](), IsSSE: true, QueryParams: []string{"cursor", "filter"}}
	var b strings.Builder
	writeRouteKotlinSSEFunc(&r, &b, []string{"id"}, nil)
	assertGolden(t, "method.kt", b.String())
}

func TestWriteRouteSwiftSSEFunc(t *testing.T) {
	t.Parallel()
	r := apispec.Route{Name: "events", Path: "/items/{id}/events", Resp: reflect.TypeFor[testSDKEvent](), IsSSE: true, QueryParams: []string{"cursor", "filter"}}
	var b strings.Builder
	writeRouteSwiftSSEFunc(&r, &b, []string{"id"}, nil)
	assertGolden(t, "method.swift", b.String())
}
