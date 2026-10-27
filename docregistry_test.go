// Tests for SDK output generation methods.

package apisdkgen

import (
	"encoding/json"
	"flag"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/maruel/apisdkgen/apispec"
)

type testJSONRPCRequest struct{}

type testJSONRPCResponse struct{}

type testSDKEvent struct {
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
}

type testSSEError struct {
	Message string `json:"message"`
}

type testSSECheckpoint struct {
	Cursor string `json:"cursor"`
}

type testSSEReset struct {
	BootstrapURL string `json:"bootstrapURL"`
}

// Task reproduces API DTO names colliding with Swift's concurrency type.
type Task struct {
	ID string `json:"id"`
}

type testPathOnlyRequest struct {
	ID string `json:"-" path:"id"`
}

type testTSTaggedRequest struct {
	ID     string `json:"-" path:"id"`
	IDs    []int  `query:"ids"`
	Hidden string `json:"-"`
	Name   string `json:"name"`
}

type testTSEmbeddedQuotas struct {
	Limit int `json:"limit"`
}

type testTSEmbeddedIdentity struct {
	ID string `json:"id"`
}

type testTSEmbeddedResponse struct {
	testTSEmbeddedQuotas
	testTSEmbeddedIdentity
	Name string `json:"name"`
}

type testJSONShadow struct {
	testTSEmbeddedQuotas
	Limit string `json:"limit"`
}

type testJSONSameName struct {
	Limit string `json:"limit"`
}

type testJSONConflict struct {
	testTSEmbeddedQuotas
	testJSONSameName        //nolint:govet // Deliberately tests an encoding/json name collision.
	Name             string `json:"name"`
}

type testJSONPointerEmbed struct {
	*testTSEmbeddedQuotas
}

type testJSONForeignEmbed struct {
	apispec.ClientScope
}

type testKotlinDocumentedFields struct {
	Name string `json:"name"`
	ID   string `json:"id,omitempty"`
}

type testDocAuthKind string

type testDocProviderQuota struct {
	AuthKind testDocAuthKind `json:"authKind"`
}

type testGeneratedErrorCode string

const (
	testGeneratedCodeBadRequest testGeneratedErrorCode = "BAD_REQUEST"
	testGeneratedCodeUnknown    testGeneratedErrorCode = "UNKNOWN"
)

type testGeneratedErrorDetails struct {
	Code    testGeneratedErrorCode `json:"code"`
	Message string                 `json:"message"`
}

type testGeneratedErrorResponse struct {
	Error testGeneratedErrorDetails `json:"error"`
}

func TestGoTypeToDoc(t *testing.T) {
	t.Parallel()

	cfg := &apispec.Config[string]{
		SpecialTypes: []apispec.SpecialType{
			{Type: reflect.TypeFor[json.RawMessage](), DocType: "JSONValue"},
			{Type: reflect.TypeFor[any](), DocType: "JSONValue"},
		},
	}
	for _, tc := range []struct {
		name string
		typ  reflect.Type
		want string
	}{
		{name: "string boolean map", typ: reflect.TypeFor[map[string]bool](), want: "Record<string, boolean>"},
		{name: "string value map", typ: reflect.TypeFor[map[string]string](), want: "Record<string, string>"},
		{name: "raw JSON map", typ: reflect.TypeFor[map[string]json.RawMessage](), want: "Record<string, JSONValue>"},
		{name: "any map", typ: reflect.TypeFor[map[string]any](), want: "Record<string, JSONValue>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := goTypeToDoc(cfg, tc.typ)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("goTypeToDoc(%s) = %q, want %q", tc.typ, got, tc.want)
			}
		})
	}
}

func TestLoadDocsInDir(t *testing.T) {
	t.Parallel()

	t.Run("string alias docs", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		source := `package v1

// testAuthKind identifies an authentication method.
type testAuthKind string

const (
	// testAuthKindOAuth uses OAuth credentials.
	testAuthKindOAuth testAuthKind = "oauth"
)
`
		if err := os.WriteFile(filepath.Join(dir, "types.go"), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		docs, err := loadDocsInDir[string](dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := docs.typeDoc["testAuthKind"]; got != "testAuthKind identifies an authentication method." {
			t.Errorf("string alias doc = %q", got)
		}
		if got := docs.aliases[0].constants[0].doc; got != "testAuthKindOAuth uses OAuth credentials." {
			t.Errorf("string alias value doc = %q", got)
		}
	})
}

type testEnvelopeKind string

type testEnvelopeSource struct {
	Agent string `json:"agent"`
}

type testEnvelopePayload struct {
	Text string `json:"text"`
}

type testEnvelope struct {
	Kind   testEnvelopeKind     `json:"kind"`
	Source testEnvelopeSource   `json:"source,omitzero"`
	Label  *string              `json:"label,omitempty"`
	Text   *testEnvelopePayload `json:"text,omitempty"`
}

func TestDocRegistry(t *testing.T) {
	t.Parallel()
	t.Run("emitTSStruct", func(t *testing.T) {
		d := &docRegistry[string]{cfg: &apispec.Config[string]{
			SDKPackagePaths: map[string]struct{}{reflect.TypeFor[testJSONShadow]().PkgPath(): {}},
			SpecialTypes:    []apispec.SpecialType{{Type: reflect.TypeFor[[]int](), TSType: "string"}},
		}}

		t.Run("valid", func(t *testing.T) {
			for _, tt := range []struct {
				name   string
				typeOf reflect.Type
				want   string
			}{
				{"outer field wins", reflect.TypeFor[testJSONShadow](), "export interface testJSONShadow {\n  limit: string;\n}\n"},
				{"equal depth conflict disappears", reflect.TypeFor[testJSONConflict](), "export interface testJSONConflict {\n  name: string;\n}\n"},
			} {
				t.Run(tt.name, func(t *testing.T) {
					var b strings.Builder
					if err := d.emitTSStruct(&b, tt.typeOf); err != nil {
						t.Fatal(err)
					}
					if got := b.String(); got != tt.want {
						t.Fatalf("generated interface = %q, want %q", got, tt.want)
					}
				})
			}
			t.Run("tagged request", func(t *testing.T) {
				var b strings.Builder
				if err := d.emitTSStruct(&b, reflect.TypeFor[testTSTaggedRequest]()); err != nil {
					t.Fatal(err)
				}
				if got, want := b.String(), "export interface testTSTaggedRequest {\n  IDs: string;\n  name: string;\n}\n"; got != want {
					t.Fatalf("got %q, want %q", got, want)
				}
			})
			t.Run("embedded response", func(t *testing.T) {
				var b strings.Builder
				if err := d.emitTSStruct(&b, reflect.TypeFor[testTSEmbeddedResponse]()); err != nil {
					t.Fatal(err)
				}
				if got, want := b.String(), "export interface testTSEmbeddedResponse {\n  limit: number /* int */;\n  id: string;\n  name: string;\n}\n"; got != want {
					t.Fatalf("got %q, want %q", got, want)
				}
			})
		})
		t.Run("error", func(t *testing.T) {
			t.Run("pointer embed rejected", func(t *testing.T) {
				var b strings.Builder
				if err := d.emitTSStruct(&b, reflect.TypeFor[testJSONPointerEmbed]()); err == nil || err.Error() != "apisdkgen.testJSONPointerEmbed.testTSEmbeddedQuotas: embedded pointer fields may be absent from JSON; embed by value or give the field a JSON name" {
					t.Fatalf("expected embedded pointer error, got %v", err)
				}
			})
			t.Run("unconfigured package rejected", func(t *testing.T) {
				var b strings.Builder
				if err := d.emitTSStruct(&b, reflect.TypeFor[testJSONForeignEmbed]()); err == nil || err.Error() != `apisdkgen.testJSONForeignEmbed.ClientScope: embedded struct package "github.com/maruel/apisdkgen/apispec" is not an SDK package; add it to SDKPackagePaths or give the field a JSON name` {
					t.Fatalf("expected SDK package error, got %v", err)
				}
			})
		})
	})
	t.Run("emitTSValidator", func(t *testing.T) {
		t.Run("embedded response", func(t *testing.T) {
			d := &docRegistry[string]{cfg: &apispec.Config[string]{SDKPackagePaths: map[string]struct{}{reflect.TypeFor[testTSEmbeddedResponse]().PkgPath(): {}}}}
			var b strings.Builder
			if err := d.emitTSValidator(&b, reflect.TypeFor[testTSEmbeddedResponse]()); err != nil {
				t.Fatal(err)
			}
			assertGolden(t, "validator.ts", b.String())
		})
	})
	t.Run("emitSwiftStruct", func(t *testing.T) {
		t.Run("embedded response", func(t *testing.T) {
			d := &docRegistry[string]{cfg: &apispec.Config[string]{SDKPackagePaths: map[string]struct{}{reflect.TypeFor[testTSEmbeddedResponse]().PkgPath(): {}}}}
			var b strings.Builder
			if err := d.emitSwiftStruct(&b, reflect.TypeFor[testTSEmbeddedResponse]()); err != nil {
				t.Fatal(err)
			}
			assertGolden(t, "struct.swift", b.String())
		})
	})
	t.Run("writeDocType", func(t *testing.T) {
		t.Run("embedded response", func(t *testing.T) {
			d := &docRegistry[string]{cfg: &apispec.Config[string]{SDKPackagePaths: map[string]struct{}{reflect.TypeFor[testTSEmbeddedResponse]().PkgPath(): {}}}}
			var b strings.Builder
			if err := d.writeDocType(&b, reflect.TypeFor[testTSEmbeddedResponse]()); err != nil {
				t.Fatal(err)
			}
			assertGolden(t, "type.md", b.String())
		})
	})
	t.Run("parseStructFields", func(t *testing.T) {
		t.Run("embedded response", func(t *testing.T) {
			d := &docRegistry[string]{cfg: &apispec.Config[string]{SDKPackagePaths: map[string]struct{}{reflect.TypeFor[testTSEmbeddedResponse]().PkgPath(): {}}}}
			fields, err := d.parseStructFields(reflect.TypeFor[testTSEmbeddedResponse]())
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(fields))
			for _, f := range fields {
				got = append(got, f.jsonName)
			}
			if diff := cmp.Diff([]string{"limit", "id", "name"}, got); diff != "" {
				t.Fatalf("field names (-want +got):\n%s", diff)
			}
		})
	})

	t.Run("generateTS", func(t *testing.T) {
		t.Run("named events", func(t *testing.T) {
			t.Parallel()

			outDir := t.TempDir()
			docs := &docRegistry[string]{
				cfg: &apispec.Config[string]{
					Routes: []apispec.Route{
						{
							Name:  "events",
							Path:  "/events",
							Resp:  reflect.TypeFor[testSDKEvent](),
							IsSSE: true,
							SSEEvents: []apispec.SSEEvent{
								{Name: "ready", Handler: "onReady"},
								{Name: "reset", Handler: "onReset"},
								{Name: "error", Handler: "onHistoryError", Resp: reflect.TypeFor[testSSEError]()},
							},
						},
						{Name: "rawEvents", Path: "/raw-events", Resp: reflect.TypeFor[testSDKEvent](), IsSSE: true},
					},
					ErrorModel: apispec.ClientErrorModel{TypeName: "DifferentError"},
				},
			}
			if err := docs.generateTS(outDir); err != nil {
				t.Fatal(err)
			}
			content, err := fs.ReadFile(os.DirFS(outDir), "api.gen.ts")
			if err != nil {
				t.Fatal(err)
			}
			assertGolden(t, "api.gen.ts", string(content))

			if err := docs.generateMarkdownDoc(outDir); err != nil {
				t.Fatal(err)
			}
			content, err = fs.ReadFile(os.DirFS(outDir), "API.md")
			if err != nil {
				t.Fatal(err)
			}
			assertGolden(t, "API.md", string(content))
		})
	})
	t.Run("generateMarkdownDoc", func(t *testing.T) {
		t.Parallel()

		t.Run("enum fields retain their type and values", func(t *testing.T) {
			t.Parallel()
			outDir := t.TempDir()
			quotaType := reflect.TypeFor[testDocProviderQuota]()
			docs := &docRegistry[string]{
				cfg: &apispec.Config[string]{
					APIDocTitle:     "Test API",
					SDKPackagePaths: map[string]struct{}{quotaType.PkgPath(): {}},
					Routes: []apispec.Route{{
						Name:   "quota",
						Method: "GET",
						Path:   "/quota",
						Resp:   quotaType,
					}},
				},
				typeDoc: map[string]string{"testDocAuthKind": "testDocAuthKind identifies an authentication method."},
				aliases: []aliasInfo{{
					name: "testDocAuthKind",
					constants: []aliasConstant{
						{name: "testDocAuthKindOAuth", value: "oauth"},
						{name: "testDocAuthKindAPIKey", value: "apikey", doc: "API key credentials."},
					},
				}},
			}
			if err := docs.generateMarkdownDoc(outDir); err != nil {
				t.Fatal(err)
			}
			data, err := fs.ReadFile(os.DirFS(outDir), "API.md")
			if err != nil {
				t.Fatal(err)
			}
			assertGolden(t, "API.md", string(data))
		})
	})
	t.Run("writeKotlinClient", func(t *testing.T) {
		t.Run("MCP", func(t *testing.T) {
			t.Parallel()

			outDir := t.TempDir()
			docs := &docRegistry[string]{
				cfg: &apispec.Config[string]{
					Routes: []apispec.Route{
						{
							Name:       "mcp",
							Method:     "POST",
							Path:       "",
							Req:        reflect.TypeFor[testJSONRPCRequest](),
							Resp:       reflect.TypeFor[testJSONRPCResponse](),
							HeadersArg: true,
						},
					},
					KotlinPackage:      "com.example.mcp",
					MCPProtocolVersion: "2026-07-28",
					ErrorModel: apispec.ClientErrorModel{
						TypeName:      "JSONRPCResponse",
						KTCodeExpr:    "err.error?.code?.toString() ?: \"UNKNOWN\"",
						KTMessageExpr: "err.error?.message ?: \"\"",
						KTDetailsExpr: "null",
					},
				},
			}
			if err := docs.writeKotlinClient(outDir); err != nil {
				t.Fatal(err)
			}
			content, err := fs.ReadFile(os.DirFS(outDir), "ApiClient.kt")
			if err != nil {
				t.Fatal(err)
			}
			assertGolden(t, "ApiClient.kt", string(content))
		})
		t.Run("query routes", func(t *testing.T) {
			d := &docRegistry[string]{cfg: &apispec.Config[string]{
				KotlinPackage: "example.api",
				Routes: []apispec.Route{
					{Name: "events", Path: "/items/{id}/events", Resp: reflect.TypeFor[testSDKEvent](), IsSSE: true, QueryParams: []string{"cursor"}},
					{Name: "rawEvents", Path: "/raw-events", Resp: reflect.TypeFor[testSDKEvent](), IsSSE: true},
				},
			}}
			dir := t.TempDir()
			if err := d.writeKotlinClient(dir); err != nil {
				t.Fatal(err)
			}
			data, err := fs.ReadFile(os.DirFS(dir), "ApiClient.kt")
			if err != nil {
				t.Fatal(err)
			}
			assertGolden(t, "ApiClient.kt", string(data))
		})
		t.Run("named events", func(t *testing.T) {
			r := apispec.Route{Name: "events", Path: "/events", Resp: reflect.TypeFor[testSDKEvent](), IsSSE: true, QueryParams: []string{"cursor"}, SSEEvents: []apispec.SSEEvent{
				{Name: "checkpoint", Handler: "onCheckpoint", Resp: reflect.TypeFor[testSSECheckpoint]()},
				{Name: "stats", Handler: "onStats", Resp: reflect.TypeFor[testSDKEvent]()},
				{Name: "reset", Handler: "onReset", Resp: reflect.TypeFor[testSSEReset]()},
				{Name: "error", Handler: "onHistoryError", Resp: reflect.TypeFor[testSSEError]()},
				{Name: "ready", Handler: "onReady"},
			}}
			d := &docRegistry[string]{cfg: &apispec.Config[string]{KotlinPackage: "example.api", Routes: []apispec.Route{r}}}
			dir := t.TempDir()
			if err := d.writeKotlinClient(dir); err != nil {
				t.Fatal(err)
			}
			data, err := fs.ReadFile(os.DirFS(dir), "ApiClient.kt")
			if err != nil {
				t.Fatal(err)
			}
			assertGolden(t, "ApiClient.kt", string(data))
		})
	})
	t.Run("emitKotlinStruct", func(t *testing.T) {
		t.Parallel()

		t.Run("path only request", func(t *testing.T) {
			t.Parallel()

			docs := &docRegistry[string]{}
			var b strings.Builder
			if err := docs.emitKotlinStruct(&b, reflect.TypeFor[testPathOnlyRequest]()); err != nil {
				t.Fatal(err)
			}
			got := b.String()
			want := "@Serializable\nclass testPathOnlyRequest\n"
			if got != want {
				t.Fatalf("emitKotlinStruct() = %q, want %q", got, want)
			}
		})

		t.Run("field docs", func(t *testing.T) {
			t.Parallel()

			docs := &docRegistry[string]{
				cfg: &apispec.Config[string]{},
				fieldDoc: map[string]map[string]string{
					"testKotlinDocumentedFields": {
						"Name": "Name is the display name.",
						"ID":   "ID is optional.",
					},
				},
			}
			var b strings.Builder
			if err := docs.emitKotlinStruct(&b, reflect.TypeFor[testKotlinDocumentedFields]()); err != nil {
				t.Fatal(err)
			}
			assertGolden(t, "struct.kt", b.String())
		})
	})
	t.Run("generateTSValidate", func(t *testing.T) {
		t.Parallel()

		t.Run("without SSE routes removes stale file", func(t *testing.T) {
			t.Parallel()

			outDir := t.TempDir()
			validatePath := filepath.Join(outDir, "validate.gen.ts")
			if err := os.WriteFile(validatePath, []byte("stale"), 0o600); err != nil {
				t.Fatal(err)
			}

			docs := &docRegistry[string]{
				cfg: &apispec.Config[string]{},
			}
			if err := docs.generateTSValidate(outDir); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(validatePath); !os.IsNotExist(err) {
				t.Fatalf("validate.gen.ts exists after generation without SSE Routes: %v", err)
			}
		})

		t.Run("with SSE routes writes validator", func(t *testing.T) {
			t.Parallel()

			outDir := t.TempDir()
			eventType := reflect.TypeFor[testSDKEvent]()
			docs := &docRegistry[string]{
				cfg: &apispec.Config[string]{
					Routes: []apispec.Route{{Name: "events", Resp: eventType, IsSSE: true}},
					SDKPackagePaths: map[string]struct{}{
						eventType.PkgPath(): {},
					},
				},
				aliasNames: map[string]struct{}{},
			}

			if err := docs.generateTSValidate(outDir); err != nil {
				t.Fatal(err)
			}
			content, err := fs.ReadFile(os.DirFS(outDir), "validate.gen.ts")
			if err != nil {
				t.Fatal(err)
			}
			assertGolden(t, "validate.gen.ts", string(content))
		})

		t.Run("discriminated common fields", func(t *testing.T) {
			t.Parallel()
			d := &docRegistry[string]{
				cfg: &apispec.Config[string]{
					Routes:          []apispec.Route{{Name: "events", Resp: reflect.TypeFor[testEnvelope](), IsSSE: true}},
					SDKPackagePaths: map[string]struct{}{reflect.TypeFor[testEnvelope]().PkgPath(): {}},
					Discriminated:   []string{"testEnvelope"},
				},
				aliases:    []aliasInfo{{name: "testEnvelopeKind", constants: []aliasConstant{{name: "Text", value: "text"}}}},
				aliasNames: map[string]struct{}{"testEnvelopeKind": {}},
			}
			dir := t.TempDir()
			if err := d.generateTSValidate(dir); err != nil {
				t.Fatal(err)
			}
			data, err := fs.ReadFile(os.DirFS(dir), "validate.gen.ts")
			if err != nil {
				t.Fatal(err)
			}
			assertGolden(t, "validate.gen.ts", string(data))
		})
		t.Run("discriminated without enum", func(t *testing.T) {
			t.Parallel()
			d := &docRegistry[string]{cfg: &apispec.Config[string]{
				Routes:          []apispec.Route{{Name: "events", Resp: reflect.TypeFor[testEnvelope](), IsSSE: true}},
				Discriminated:   []string{"testEnvelope"},
				SDKPackagePaths: map[string]struct{}{reflect.TypeFor[testEnvelope]().PkgPath(): {}},
			}}
			dir := t.TempDir()
			if err := d.generateTSValidate(dir); err != nil {
				t.Fatal(err)
			}
			data, err := fs.ReadFile(os.DirFS(dir), "validate.gen.ts")
			if err != nil {
				t.Fatal(err)
			}
			assertGolden(t, "validate.gen.ts", string(data))
		})
	})
	t.Run("writeSwiftClient", func(t *testing.T) {
		t.Run("query routes", func(t *testing.T) {
			d := &docRegistry[string]{cfg: &apispec.Config[string]{
				KotlinPackage: "example.api",
				Routes: []apispec.Route{
					{Name: "events", Path: "/items/{id}/events", Resp: reflect.TypeFor[testSDKEvent](), IsSSE: true, QueryParams: []string{"cursor"}},
					{Name: "rawEvents", Path: "/raw-events", Resp: reflect.TypeFor[testSDKEvent](), IsSSE: true},
				},
			}}
			dir := t.TempDir()
			if err := d.writeSwiftClient(dir); err != nil {
				t.Fatal(err)
			}
			data, err := fs.ReadFile(os.DirFS(dir), "ApiClient.swift")
			if err != nil {
				t.Fatal(err)
			}
			assertGolden(t, "ApiClient.swift", string(data))
		})
		t.Run("named events", func(t *testing.T) {
			r := apispec.Route{Name: "events", Path: "/events", Resp: reflect.TypeFor[testSDKEvent](), IsSSE: true, QueryParams: []string{"cursor"}, SSEEvents: []apispec.SSEEvent{
				{Name: "checkpoint", Handler: "onCheckpoint", Resp: reflect.TypeFor[testSSECheckpoint]()},
				{Name: "stats", Handler: "onStats", Resp: reflect.TypeFor[testSDKEvent]()},
				{Name: "reset", Handler: "onReset", Resp: reflect.TypeFor[testSSEReset]()},
				{Name: "error", Handler: "onHistoryError", Resp: reflect.TypeFor[testSSEError]()},
				{Name: "ready", Handler: "onReady"},
			}}
			d := &docRegistry[string]{cfg: &apispec.Config[string]{KotlinPackage: "example.api", Routes: []apispec.Route{r}}}
			dir := t.TempDir()
			if err := d.writeSwiftClient(dir); err != nil {
				t.Fatal(err)
			}
			data, err := fs.ReadFile(os.DirFS(dir), "ApiClient.swift")
			if err != nil {
				t.Fatal(err)
			}
			assertGolden(t, "ApiClient.swift", string(data))
		})
	})
}

func TestGenerate(t *testing.T) {
	t.Parallel()
	t.Run("configured error code type", func(t *testing.T) {
		t.Parallel()

		errorResponseType := reflect.TypeFor[testGeneratedErrorResponse]()
		cfg := apispec.Config[testGeneratedErrorCode]{
			SDKPackagePaths: map[string]struct{}{errorResponseType.PkgPath(): {}},
			ExtraSeeds:      []reflect.Type{errorResponseType},
			ErrorModel:      apispec.ClientErrorModel{TypeName: errorResponseType.Name()},
			ErrorCodes: []apispec.ErrorCodeSpec[testGeneratedErrorCode]{
				{Code: testGeneratedCodeBadRequest, Status: 400},
				{Code: testGeneratedCodeUnknown, Status: 500},
			},
		}
		tsDir := t.TempDir()
		kotlinDir := t.TempDir()
		swiftDir := t.TempDir()
		api := NewAPI(".", OutputConfig{
			TypeScriptDir: tsDir,
			KotlinDir:     kotlinDir,
			SwiftDir:      swiftDir,
		}, cfg)
		if err := Generate(&api); err != nil {
			t.Fatal(err)
		}

		for _, file := range []struct{ dir, name string }{
			{tsDir, "types.gen.ts"}, {tsDir, "api.gen.ts"},
			{kotlinDir, "Types.kt"}, {kotlinDir, "ApiClient.kt"},
			{swiftDir, "Types.swift"}, {swiftDir, "ApiClient.swift"},
		} {
			t.Run(file.name, func(t *testing.T) {
				data, err := fs.ReadFile(os.DirFS(file.dir), file.name)
				if err != nil {
					t.Fatal(err)
				}
				assertGolden(t, file.name, string(data))
			})
		}
	})

	t.Run("empty TypeScript client", func(t *testing.T) {
		t.Parallel()
		_, err := exec.LookPath("tsc")
		if err != nil {
			t.Fatalf("TypeScript compiler is required: %v", err)
		}
		for _, tc := range []struct {
			name       string
			typedError bool
		}{
			{name: "string error code"},
			{name: "typed error code", typedError: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				cfg := apispec.Config[testGeneratedErrorCode]{
					SDKPackagePaths: map[string]struct{}{reflect.TypeFor[testGeneratedErrorResponse]().PkgPath(): {}},
					ExtraSeeds:      []reflect.Type{reflect.TypeFor[testGeneratedErrorResponse]()},
					ErrorModel:      apispec.ClientErrorModel{TypeName: "testGeneratedErrorResponse"},
				}
				if tc.typedError {
					cfg.ErrorCodes = []apispec.ErrorCodeSpec[testGeneratedErrorCode]{{Code: testGeneratedCodeUnknown, Status: 500}}
				}
				dir := t.TempDir()
				api := NewAPI(".", OutputConfig{TypeScriptDir: dir}, cfg)
				if err := Generate(&api); err != nil {
					t.Fatal(err)
				}
				consumer := `import { APIError, createApiClient } from "./api.gen";
const client = createApiClient(async () => new Response("{}"));
console.log(client, new APIError(500, "UNKNOWN"));
`
				path := filepath.Join(dir, "consumer.ts")
				if err := os.WriteFile(path, []byte(consumer), 0o600); err != nil {
					t.Fatal(err)
				}
				cmd := exec.CommandContext(t.Context(), "tsc", "--noEmit", "--strict", "--noUnusedLocals", "--noUnusedParameters", "--target", "ES2022", "--module", "ESNext", "--moduleResolution", "bundler", "consumer.ts")
				cmd.Dir = dir
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("strict consumer compilation failed: %v\n%s", err, out)
				}
			})
		}
	})
	t.Run("TypeScript SSE runtime", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		eventType := reflect.TypeFor[testSDKEvent]()
		cfg := apispec.Config[string]{
			SDKPackagePaths: map[string]struct{}{eventType.PkgPath(): {}},
			ExtraSeeds:      []reflect.Type{reflect.TypeFor[testGeneratedErrorResponse]()},
			ErrorModel:      apispec.ClientErrorModel{TypeName: "testGeneratedErrorResponse", TSDetailsExpr: "undefined"},
			ClientScopes:    []apispec.ClientScope{{Name: "workspace", PathPrefix: "/workspaces/{wsID}", Parameter: "wsID"}},
			Routes: []apispec.Route{
				{Name: "events", Path: "/workspaces/{wsID}/items/{id}/events", Resp: eventType, IsSSE: true, QueryParams: []string{"cursor", "filter"}, SSEEvents: []apispec.SSEEvent{
					{Name: "checkpoint", Handler: "onCheckpoint", Resp: reflect.TypeFor[testSSECheckpoint]()},
					{Name: "stats", Handler: "onStats", Resp: eventType},
					{Name: "reset", Handler: "onReset", Resp: reflect.TypeFor[testSSEReset]()},
					{Name: "ready", Handler: "onReady"},
					{Name: "failure", Handler: "onFailure", Resp: reflect.TypeFor[testSSEError]()},
				}},
				{Name: "rootEvents", Path: "/events", Resp: eventType, IsSSE: true, QueryParams: []string{"cursor"}},
				{Name: "rawEvents", Path: "/raw-events", Resp: eventType, IsSSE: true},
			},
		}
		api := NewAPI(".", OutputConfig{TypeScriptDir: dir}, cfg)
		if err := Generate(&api); err != nil {
			t.Fatal(err)
		}
		consumer := `import { createApiClient } from "./api.gen";
class Source extends EventTarget {
  constructor(readonly url: string) { super(); }
}

globalThis.EventSource = Source as unknown as typeof EventSource;
function check(ok: boolean): void { if (!ok) throw new Error("SSE contract failed"); }
const client = createApiClient(async () => { throw new Error("unexpected JSON request"); });
const value = "a +&=/#?%雪";
let messages = 0, ready = 0, failures = 0, errors = 0;
const controls: string[] = [];
const handlers = {
  onMessage: (event: { kind: string }) => { check(event.kind === "update"); messages++; controls.push("message"); },
  onCheckpoint: (event: { cursor: string }) => { check(event.cursor === "next"); controls.push("checkpoint"); },
  onStats: (event: { kind: string }) => { check(event.kind === "stats"); controls.push("stats"); },
  onReset: (event: { bootstrapURL: string }) => { check(event.bootstrapURL === "/bootstrap"); controls.push("reset"); },
  onReady: () => { ready++; },
  onFailure: (event: { message: string }) => { check(event.message === "expired"); failures++; },
  onError: (_error: unknown) => { errors++; },
};
const source = client.workspace(value).events(value, value, "", handlers);
check(source instanceof Source);
const url = new URL(source.url, "https://example.test");
check(url.pathname === "/workspaces/" + encodeURIComponent(value) + "/items/" + encodeURIComponent(value) + "/events");
check(url.searchParams.get("cursor") === value && url.searchParams.get("filter") === "");
check(url.searchParams.size === 2 && url.hash === "");
source.dispatchEvent(new MessageEvent("checkpoint", { data: JSON.stringify({cursor: "next"}), lastEventId: "next" }));
source.dispatchEvent(new MessageEvent("message", { data: JSON.stringify({kind: "update"}) }));
source.dispatchEvent(new MessageEvent("stats", { data: JSON.stringify({kind: "stats"}) }));
source.dispatchEvent(new MessageEvent("reset", { data: JSON.stringify({bootstrapURL: "/bootstrap"}) }));
source.dispatchEvent(new Event("ready"));
source.dispatchEvent(new MessageEvent("failure", { data: JSON.stringify({message: "expired"}) }));
source.dispatchEvent(new MessageEvent("message", { data: "not JSON" }));
check(messages === 1 && ready === 1 && failures === 1 && errors === 1);
check(controls.join(",") === "checkpoint,message,stats,reset");
check(new URL(client.rootEvents(value, handlers).url, "https://example.test").searchParams.get("cursor") === value);
check(client.rootEvents("", handlers).url === "/events?cursor=");
check(client.rawEvents(handlers).url === "/raw-events");
// @ts-expect-error A configured scalar query argument is required.
if (false) client.rootEvents(handlers);
// @ts-expect-error Queries do not add arguments to routes without QueryParams.
if (false) client.rawEvents("", handlers);
`
		if err := os.WriteFile(filepath.Join(dir, "consumer.ts"), []byte(consumer), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, cmd := range []*exec.Cmd{
			exec.CommandContext(t.Context(), "tsc", "--strict", "--target", "ES2022", "--module", "commonjs", "consumer.ts"),
			exec.CommandContext(t.Context(), "node", "consumer.js"),
		} {
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s failed: %v\n%s", cmd.Path, err, out)
			}
		}
	})
	t.Run("Kotlin SSE runtime", func(t *testing.T) {
		// Optional native execution uses the caller's installed Kotlin compiler and
		// OkHttp, Okio, coroutine and serialization jars. The normal suite always
		// checks the emitted native API even when these dependencies are absent.
		cp := os.Getenv("APISDKGEN_KOTLIN_CLASSPATH")
		if cp == "" {
			t.Skip("set APISDKGEN_KOTLIN_CLASSPATH to run the generated Kotlin HTTP regression")
		}
		_, err := exec.LookPath("kotlinc")
		if err != nil {
			t.Fatal(err)
		}
		plugin := os.Getenv("APISDKGEN_KOTLIN_SERIALIZATION_PLUGIN")
		if plugin == "" {
			t.Fatal("APISDKGEN_KOTLIN_SERIALIZATION_PLUGIN is required")
		}
		dir := t.TempDir()
		eventType := reflect.TypeFor[testSDKEvent]()
		cfg := apispec.Config[string]{
			SDKPackagePaths: map[string]struct{}{eventType.PkgPath(): {}},
			KotlinPackage:   "nativecheck",
			ErrorModel:      apispec.ClientErrorModel{TypeName: "testSSEError", KTCodeExpr: `"UNKNOWN"`, KTMessageExpr: "err.message", KTDetailsExpr: "null"},
			Routes: []apispec.Route{{Name: "events", Path: "/events", Resp: eventType, IsSSE: true, QueryParams: []string{"cursor"}, SSEEvents: []apispec.SSEEvent{
				{Name: "checkpoint", Handler: "onCheckpoint", Resp: reflect.TypeFor[testSSECheckpoint]()},
				{Name: "stats", Handler: "onStats", Resp: eventType},
				{Name: "reset", Handler: "onReset", Resp: reflect.TypeFor[testSSEReset]()},
				{Name: "error", Handler: "onHistoryError", Resp: reflect.TypeFor[testSSEError]()},
				{Name: "ready", Handler: "onReady"},
			}}},
		}
		api := NewAPI(".", OutputConfig{KotlinDir: dir}, cfg)
		if err := Generate(&api); err != nil {
			t.Fatal(err)
		}
		fixture, err := filepath.Abs("testdata/SSEStreamTest.kt")
		if err != nil {
			t.Fatal(err)
		}
		jar := filepath.Join(dir, "runtime.jar")
		// These executables and compiler flags are fixed; only the locally installed
		// dependency paths and generated temporary files vary.
		// #nosec G204 G702 -- Local developer-supplied compiler dependencies are intentional test inputs; no shell is used.
		cmd := exec.CommandContext(t.Context(), "kotlinc", "-cp", cp, "-Xplugin="+plugin, filepath.Join(dir, "ApiClient.kt"), filepath.Join(dir, "Types.kt"), fixture, "-include-runtime", "-d", jar)
		if !t.Run("compile", func(t *testing.T) {
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("Kotlin compilation failed: %v\n%s", err, out)
			}
		}) {
			return
		}
		// #nosec G204 G702 -- Run the generated test jar with the same trusted local dependency classpath, without a shell.
		cmd = exec.CommandContext(t.Context(), "java", "-cp", jar+string(os.PathListSeparator)+cp, "nativecheck.SSEStreamTestKt")
		t.Run("run", func(t *testing.T) {
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("Kotlin HTTP runtime failed: %v\n%s", err, out)
			}
		})
	})
	t.Run("Swift SSE runtime", func(t *testing.T) {
		if runtime.GOOS != "linux" {
			t.Skip("fixture supplies the Linux FoundationNetworking byte transport")
		}
		if _, err := exec.LookPath("swiftc"); err != nil {
			t.Skip("Swift compiler is optional")
		}
		dir := t.TempDir()
		eventType := reflect.TypeFor[testSDKEvent]()
		cfg := apispec.Config[string]{
			SDKPackagePaths: map[string]struct{}{eventType.PkgPath(): {}},
			ExtraSeeds:      []reflect.Type{reflect.TypeFor[Task]()},
			ErrorModel:      apispec.ClientErrorModel{TypeName: "testSSEError", SwiftCodeExpr: `"UNKNOWN"`, SwiftMessageExpr: "errResp.message", SwiftDetailsExpr: "nil"},
			Routes: []apispec.Route{{Name: "events", Path: "/events", Resp: eventType, IsSSE: true, QueryParams: []string{"cursor"}, SSEEvents: []apispec.SSEEvent{
				{Name: "checkpoint", Handler: "onCheckpoint", Resp: reflect.TypeFor[testSSECheckpoint]()},
				{Name: "stats", Handler: "onStats", Resp: eventType},
				{Name: "reset", Handler: "onReset", Resp: reflect.TypeFor[testSSEReset]()},
				{Name: "error", Handler: "onHistoryError", Resp: reflect.TypeFor[testSSEError]()},
				{Name: "ready", Handler: "onReady"},
			}}},
		}
		api := NewAPI(".", OutputConfig{SwiftDir: dir}, cfg)
		if err := Generate(&api); err != nil {
			t.Fatal(err)
		}
		fixture, err := filepath.Abs("testdata/SSEStreamTest.swift")
		if err != nil {
			t.Fatal(err)
		}
		binary := filepath.Join(dir, "runtime")
		// #nosec G204 -- Fixed compiler and flags consume only generated temporary files and the repository fixture.
		cmd := exec.CommandContext(t.Context(), "swiftc", "-swift-version", "5", "-parse-as-library", filepath.Join(dir, "ApiClient.swift"), filepath.Join(dir, "Types.swift"), fixture, "-o", binary)
		if !t.Run("compile", func(t *testing.T) {
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("Swift compilation failed: %v\n%s", err, out)
			}
		}) {
			return
		}
		// #nosec G204 -- Execute only the test binary compiled above in this test's private temporary directory.
		cmd = exec.CommandContext(t.Context(), binary)
		t.Run("run", func(t *testing.T) {
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("Swift runtime failed: %v\n%s", err, out)
			}
		})
	})
}

var updateGolden = flag.Bool("update", false, "update generated-output golden expectations")

func assertGolden(t *testing.T, name, got string) {
	path := filepath.Join("testdata", "golden", t.Name(), name+".golden")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		// #nosec G703 -- The path uses only repository-defined test and fixture names; -update explicitly authorizes writing expectations.
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := fs.ReadFile(os.DirFS("."), filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(string(want), got); diff != "" {
		t.Errorf("%s mismatch (-want +got):\n%s", path, diff)
	}
}
