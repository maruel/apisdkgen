// Tests for SDK route method generation and scoped URL paths.
package apisdkgen

import (
	"io/fs"
	"os"
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
	d := &docRegistry[string]{cfg: &apispec.Config[string]{}}
	if err := writeRouteTSJSONMethod(d, &route, &b, []string{"id"}); err != nil {
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
	if err := writeRouteTSJSONMethod(d, &route, &strings.Builder{}, []string{"id"}); err == nil {
		t.Fatal("expected missing query field error")
	}
}

func TestEmbeddedQueryRequest(t *testing.T) {
	request := reflect.TypeFor[testEmbeddedQueryRequest]()
	d := &docRegistry[string]{cfg: &apispec.Config[string]{
		SDKPackagePaths: map[string]struct{}{request.PkgPath(): {}},
	}}
	route := apispec.Route{
		Name: "list", Method: "GET", Path: "/items",
		Req: request, Resp: request, QueryFromReq: true, QueryParams: []string{"limit"},
	}
	var b strings.Builder
	if err := writeRouteTSJSONMethod(d, &route, &b, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `query.set("limit", String(req.limit))`) {
		t.Fatalf("promoted query field not emitted: %s", b.String())
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

func TestSSEQueryMethods(t *testing.T) {
	r := apispec.Route{Name: "events", Path: "/items/{id}/events", Resp: reflect.TypeFor[testSDKEvent](), IsSSE: true, QueryParams: []string{"cursor", "filter"}}
	for _, tc := range []struct {
		name  string
		write func(*apispec.Route, *strings.Builder, []string)
		want  []string
	}{
		{"TypeScript", writeRouteTSSSEMethod, []string{"events: (id: string, cursor: string, filter: string, handlers: EventsHandlers)", "cursor=${encodeURIComponent(cursor)}&filter=${encodeURIComponent(filter)}"}},
		{"Kotlin", writeRouteKotlinSSEFunc, []string{"fun events(id: String, cursor: String, filter: String)", `cursor=${java.net.URLEncoder.encode(cursor, "UTF-8")}&filter=${java.net.URLEncoder.encode(filter, "UTF-8")}`}},
		{"Swift", writeRouteSwiftSSEFunc, []string{"func events(id: String, cursor: String, filter: String)", `cursor=\(cursor.addingPercentEncoding`, `&filter=\(filter.addingPercentEncoding`, `CharacterSet(charactersIn: "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~")`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b strings.Builder
			tc.write(&r, &b, []string{"id"})
			for _, want := range tc.want {
				if !strings.Contains(b.String(), want) {
					t.Errorf("missing %q:\n%s", want, b.String())
				}
			}
		})
	}
}

func TestSSEQueryNativeClients(t *testing.T) {
	d := &docRegistry[string]{cfg: &apispec.Config[string]{
		KotlinPackage: "example.api",
		Routes: []apispec.Route{
			{Name: "events", Path: "/items/{id}/events", Resp: reflect.TypeFor[testSDKEvent](), IsSSE: true, QueryParams: []string{"cursor"}},
			{Name: "rawEvents", Path: "/raw-events", Resp: reflect.TypeFor[testSDKEvent](), IsSSE: true},
		},
	}}
	for _, tc := range []struct {
		name  string
		write func(string) error
		file  string
		want  []string
	}{
		{"Kotlin", d.writeKotlinClient, "ApiClient.kt", []string{
			`fun events(id: String, cursor: String): Flow<testSDKEvent>`,
			`/items/$id/events?cursor=${java.net.URLEncoder.encode(cursor, "UTF-8")}`,
			`fun eventsReconnecting(id: String, cursor: String): Flow<testSDKEvent> = reconnectingFlow { events(id, cursor) }`,
			`fun rawEvents(): Flow<testSDKEvent> = sseFlow<testSDKEvent>("/raw-events")`,
			`fun rawEventsReconnecting(): Flow<testSDKEvent> = reconnectingFlow { rawEvents() }`,
		}},
		{"Swift", d.writeSwiftClient, "ApiClient.swift", []string{
			`func events(id: String, cursor: String) -> AsyncThrowingStream<testSDKEvent, Error>`,
			`/items/\(id)/events?cursor=\(cursor.addingPercentEncoding`,
			`func eventsReconnecting(id: String, cursor: String) -> AsyncThrowingStream<testSDKEvent, Error>`,
			`reconnectingStream { self.events(id: id, cursor: cursor) }`,
			`func rawEvents() -> AsyncThrowingStream<testSDKEvent, Error>`,
			`sseStream(path: "/raw-events")`,
			`reconnectingStream { self.rawEvents() }`,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := tc.write(dir); err != nil {
				t.Fatal(err)
			}
			content, err := fs.ReadFile(os.DirFS(dir), tc.file)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.want {
				if !strings.Contains(string(content), want) {
					t.Errorf("missing %q:\n%s", want, content)
				}
			}
		})
	}
}

func TestSSENativeNamedEvents(t *testing.T) {
	r := apispec.Route{Name: "events", Path: "/events", Resp: reflect.TypeFor[testSDKEvent](), IsSSE: true, QueryParams: []string{"cursor"}, SSEEvents: []apispec.SSEEvent{
		{Name: "checkpoint", Handler: "onCheckpoint", Resp: reflect.TypeFor[testSSECheckpoint]()},
		{Name: "stats", Handler: "onStats", Resp: reflect.TypeFor[testSDKEvent]()},
		{Name: "reset", Handler: "onReset", Resp: reflect.TypeFor[testSSEReset]()},
		{Name: "error", Handler: "onHistoryError", Resp: reflect.TypeFor[testSSEError]()},
		{Name: "ready", Handler: "onReady"},
	}}
	d := &docRegistry[string]{cfg: &apispec.Config[string]{KotlinPackage: "example.api", Routes: []apispec.Route{r}}}
	for _, tc := range []struct {
		name  string
		write func(string) error
		file  string
		want  []string
	}{
		{"Kotlin", d.writeKotlinClient, "ApiClient.kt", []string{
			`onCheckpoint: ((testSSECheckpoint) -> Unit)? = null`,
			`"message" -> json.decodeFromString<testSDKEvent>(data)`,
			`"checkpoint" -> { val payload = json.decodeFromString<testSSECheckpoint>(data); onCheckpoint?.invoke(payload); null }`,
			`"stats" -> { val payload = json.decodeFromString<testSDKEvent>(data); onStats?.invoke(payload); null }`,
			`"reset" -> { val payload = json.decodeFromString<testSSEReset>(data); onReset?.invoke(payload); null }`,
			`"error" -> { val payload = json.decodeFromString<testSSEError>(data); onHistoryError?.invoke(payload); null }`,
			`"ready" -> { onReady?.invoke(); null }`,
			`eventID?.let { onEventID?.invoke(it) }`,
			`if (event != null) send(event)`,
			`if (lastEventID != null) header("Last-Event-ID", lastEventID)`,
			`"id" -> if (!value.contains('\u0000')) eventID = value`,
			`eventID = null`,
			`data.append(value).append('\n')`,
			`if (maxSSEEventBytes != null && lineBytes.size >= maxSSEEventBytes)`,
			`if (dataSize > maxSSEEventBytes)`,
			`lastEventID = resumeID.get()`,
			`onEventID = { resumeID.set(it); onEventID?.invoke(it) }`,
		}},
		{"Swift", d.writeSwiftClient, "ApiClient.swift", []string{
			`onCheckpoint: ((testSSECheckpoint) -> Void)? = nil`,
			`case "message": return try self.decoder.decode(testSDKEvent.self, from: data)`,
			`case "checkpoint": let payload = try self.decoder.decode(testSSECheckpoint.self, from: data); onCheckpoint?(payload); return nil`,
			`case "stats": let payload = try self.decoder.decode(testSDKEvent.self, from: data); onStats?(payload); return nil`,
			`case "reset": let payload = try self.decoder.decode(testSSEReset.self, from: data); onReset?(payload); return nil`,
			`case "error": let payload = try self.decoder.decode(testSSEError.self, from: data); onHistoryError?(payload); return nil`,
			`case "ready": onReady?(); return nil`,
			`if let lastEventID { req.setValue(lastEventID, forHTTPHeaderField: "Last-Event-ID") }`,
			`return AsyncThrowingStream(unfolding: { try await reader.next() })`,
			`iterator = nil // Clean EOF reconnects with the latest cursor.`,
			`} catch is SSETransportFailure {`,
			`if let eventID { onEventID?(eventID) }`,
			`eventType = "message"`,
			`eventID = nil`,
			`dataLines.joined(separator: "\n")`,
			`case "id": if !value.contains("\0") { eventID = value }`,
			`if let limit = maxSSEEventBytes, lineBytes.count >= limit`,
			`if dataSize > limit`,
			`lastEventID: resumeID.get()`,
			`onEventID: { resumeID.set($0); onEventID?($0) }`,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := tc.write(dir); err != nil {
				t.Fatal(err)
			}
			content, err := fs.ReadFile(os.DirFS(dir), tc.file)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.want {
				if !strings.Contains(string(content), want) {
					t.Errorf("missing %q:\n%s", want, content)
				}
			}
			if tc.name == "Swift" {
				text := string(content)
				for _, bounds := range [][2]string{
					{"private final class SSEFrameReader", "private final class SSECursor"},
					{"private func namedSSEStream", "private func sseStream"},
					{"public func eventsReconnecting", "\n    }"},
				} {
					_, body, ok := strings.Cut(text, bounds[0])
					if !ok {
						t.Fatalf("missing %s", bounds[0])
					}
					body, _, ok = strings.Cut(body, bounds[1])
					if !ok {
						t.Fatalf("missing end %s", bounds[1])
					}
					for _, forbidden := range []string{".yield(", "Task {", "bufferingOldest", "bufferingNewest"} {
						if strings.Contains(body, forbidden) {
							t.Errorf("declared Swift pipeline must pull losslessly, found %q", forbidden)
						}
					}
				}
			}
		})
	}
}
