// Tests for TypeScript route method generation and scoped URL paths.
package apisdkgen

import (
	"reflect"
	"strings"
	"testing"

	"github.com/maruel/apisdkgen/apispec"
)

func TestScopedQueryRequest(t *testing.T) {
	type query struct {
		Limit int  `json:"limit" query:"limit"`
		Only  bool `json:"unread_only" query:"unread_only"`
	}
	route := apispec.Route{
		Name: "list", Method: "GET", Path: "/api/v1/workspaces/{wsID}/items/{id}",
		Req: reflect.TypeFor[query](), Resp: reflect.TypeFor[query](),
		QueryFromReq: true, QueryParams: []string{"limit", "unread_only"},
	}
	var b strings.Builder
	if err := writeRouteTSJSONMethod(&route, &b, []string{"id"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"list: (id: string, req: query)",
		"query.set(\"limit\", String(req.limit))",
		"query.set(\"unread_only\", String(req.unread_only))",
		"/api/v1/workspaces/${encodeURIComponent(wsID)}/items/${encodeURIComponent(id)}",
		"query.size ? `?${query}` : \"\"",
	} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("generated method missing %q:\n%s", want, b.String())
		}
	}
	if strings.Contains(b.String(), ", req)") {
		t.Errorf("GET request must not send a JSON body: %s", b.String())
	}
	route.QueryParams = append(route.QueryParams, "missing")
	if err := writeRouteTSJSONMethod(&route, &strings.Builder{}, []string{"id"}); err == nil {
		t.Fatal("expected missing query field error")
	}
}

func TestRouteClientScopeBoundary(t *testing.T) {
	scopes := []apispec.ClientScope{{Name: "ws", PathPrefix: "/api/v1/workspaces/{wsID}", Parameter: "wsID"}}
	if got := routeClientScope(scopes, "/api/v1/workspaces/{wsID}/items"); got == nil || got.Name != "ws" {
		t.Fatalf("expected workspace scope, got %v", got)
	}
	if got := routeClientScope(scopes, "/api/v1/workspaces/{wsID}extra"); got != nil {
		t.Fatalf("unexpected scope: %v", got)
	}
}
