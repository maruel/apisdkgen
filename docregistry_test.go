// Tests for SDK output generation methods.

package apisdkgen

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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

func TestJSONFieldGeneration(t *testing.T) {
	t.Parallel()
	d := &docRegistry[string]{cfg: &apispec.Config[string]{
		SDKPackagePaths: map[string]struct{}{reflect.TypeFor[testTSEmbeddedResponse]().PkgPath(): {}},
		SpecialTypes:    []apispec.SpecialType{{Type: reflect.TypeFor[[]int](), TSType: "string"}},
	}}
	var b strings.Builder
	if err := d.emitTSStruct(&b, reflect.TypeFor[testTSTaggedRequest]()); err != nil {
		t.Fatal(err)
	}
	if got, want := b.String(), "export interface testTSTaggedRequest {\n  IDs: string;\n  name: string;\n}\n"; got != want {
		t.Fatalf("generated request = %q, want %q", got, want)
	}
	b.Reset()
	if err := d.emitTSStruct(&b, reflect.TypeFor[testTSEmbeddedResponse]()); err != nil {
		t.Fatal(err)
	}
	if got, want := b.String(), "export interface testTSEmbeddedResponse {\n  limit: number /* int */;\n  id: string;\n  name: string;\n}\n"; got != want {
		t.Fatalf("generated response = %q, want %q", got, want)
	}
	b.Reset()
	if err := d.emitTSValidator(&b, reflect.TypeFor[testTSEmbeddedResponse]()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "limit: asNumber(obj[\"limit\"]") || strings.Contains(b.String(), "testTSEmbeddedQuotas:") {
		t.Fatalf("validator did not flatten fields: %s", b.String())
	}
	fields, err := d.parseStructFields(reflect.TypeFor[testTSEmbeddedResponse]())
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 3 || fields[0].jsonName != "limit" || fields[1].jsonName != "id" || fields[2].jsonName != "name" {
		t.Fatalf("Kotlin fields did not flatten: %+v", fields)
	}
	b.Reset()
	if err := d.emitSwiftStruct(&b, reflect.TypeFor[testTSEmbeddedResponse]()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "public let limit: Int") || strings.Contains(b.String(), "testTSEmbeddedQuotas") {
		t.Fatalf("Swift fields did not flatten: %s", b.String())
	}
	b.Reset()
	if err := d.writeDocType(&b, reflect.TypeFor[testTSEmbeddedResponse]()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "| `limit` |") || strings.Contains(b.String(), "| `testTSEmbeddedQuotas` |") {
		t.Fatalf("API docs did not flatten fields: %s", b.String())
	}
}

func TestEmbeddedJSONFieldPrecedence(t *testing.T) {
	t.Parallel()
	d := &docRegistry[string]{cfg: &apispec.Config[string]{
		SDKPackagePaths: map[string]struct{}{reflect.TypeFor[testJSONShadow]().PkgPath(): {}},
	}}
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
	t.Run("pointer embed rejected", func(t *testing.T) {
		var b strings.Builder
		if err := d.emitTSStruct(&b, reflect.TypeFor[testJSONPointerEmbed]()); err == nil || !strings.Contains(err.Error(), "embedded pointer") {
			t.Fatalf("expected embedded pointer error, got %v", err)
		}
	})
	t.Run("unconfigured package rejected", func(t *testing.T) {
		var b strings.Builder
		if err := d.emitTSStruct(&b, reflect.TypeFor[testJSONForeignEmbed]()); err == nil || !strings.Contains(err.Error(), "SDKPackagePaths") {
			t.Fatalf("expected SDK package error, got %v", err)
		}
	})
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

func TestGenConfigGoTypeToDoc(t *testing.T) {
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

func TestDocRegistryGenerateTSTypes(t *testing.T) {
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

		files := []struct {
			dir   string
			name  string
			wants []string
		}{
			{dir: tsDir, name: "types.gen.ts", wants: []string{
				"export type testGeneratedErrorCode =\n  | \"BAD_REQUEST\"\n  | \"UNKNOWN\"\n  | (string & {});",
				"export const testGeneratedErrorCodeBadRequest = \"BAD_REQUEST\";",
				"code: testGeneratedErrorCode;",
			}},
			{dir: tsDir, name: "api.gen.ts", wants: []string{
				"import type { testGeneratedErrorCode, testGeneratedErrorResponse } from \"./types.gen\";",
				"public code: testGeneratedErrorCode,",
			}},
			{dir: kotlinDir, name: "Types.kt", wants: []string{
				"sealed interface testGeneratedErrorCode {",
				"data class testGeneratedErrorDetails(val code: testGeneratedErrorCode, val message: String)",
			}},
			{dir: kotlinDir, name: "ApiClient.kt", wants: []string{
				"val code: testGeneratedErrorCode,",
				"testGeneratedErrorCode.Other(\"UNKNOWN\")",
			}},
			{dir: swiftDir, name: "Types.swift", wants: []string{
				"public struct testGeneratedErrorCode: Codable, Equatable, Hashable {",
				"public let code: testGeneratedErrorCode",
			}},
			{dir: swiftDir, name: "ApiClient.swift", wants: []string{
				"public let code: testGeneratedErrorCode",
				"testGeneratedErrorCode.other(\"UNKNOWN\")",
			}},
		}
		for _, file := range files {
			data, err := fs.ReadFile(os.DirFS(file.dir), file.name)
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			for _, want := range file.wants {
				if !strings.Contains(text, want) {
					t.Errorf("%s does not contain %q:\n%s", file.name, want, text)
				}
			}
		}
	})
}

func TestDocRegistryGenerateMarkdownDoc(t *testing.T) {
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
		text := string(data)
		for _, want := range []string{
			"### testDocAuthKind",
			"| `oauth` |  |",
			"| `apikey` | API key credentials. |",
			"| `authKind` | `testDocAuthKind` |  | yes |",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("API.md does not contain %q:\n%s", want, text)
			}
		}
	})
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

func TestDocRegistryGenerateKotlinMCPClient(t *testing.T) {
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
	text := string(content)
	for _, want := range []string{
		"private val mcpID = java.util.concurrent.atomic.AtomicInteger(0)",
		"id = kotlinx.serialization.json.JsonPrimitive(mcpID.incrementAndGet())",
		"fun subscriptionsListen(",
		"method = \"POST\"",
		"private val httpClient: OkHttpClient = OkHttpClient()",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("ApiClient.kt does not contain %q:\n%s", want, text)
		}
	}
}

func TestDocRegistryEmitKotlinStruct(t *testing.T) {
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
		got := b.String()
		for _, want := range []string{
			"    /** Name is the display name. */\n    val name: String,",
			"    /** ID is optional. */\n    val id: String? = null,",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("emitKotlinStruct() does not contain %q:\n%s", want, got)
			}
		}
	})
}

func TestDocRegistryGenerateTSNamedEvents(t *testing.T) {
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
	text := string(content)
	for _, want := range []string{
		"import type { DifferentError, testSDKEvent, testSSEError } from \"./types.gen\";",
		"export interface EventsHandlers {",
		"export interface RawEventsHandlers {",
		"onMessage: (event: testSDKEvent) => void;",
		"onError: (err: unknown) => void;",
		"onReady?: () => void;",
		"onReset?: () => void;",
		"onHistoryError?: (event: testSSEError) => void;",
		"events: (handlers: EventsHandlers): EventSource => {",
		"rawEvents: (handlers: RawEventsHandlers): EventSource => {",
		"handlers.onMessage(validatetestSDKEvent(JSON.parse(e.data)));",
		"if (!(e instanceof MessageEvent) || typeof e.data !== \"string\") return;",
		"handlers.onHistoryError?.(validatetestSSEError(JSON.parse(e.data)));",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("api.gen.ts does not contain %q:\n%s", want, text)
		}
	}

	if err := docs.generateMarkdownDoc(outDir); err != nil {
		t.Fatal(err)
	}
	content, err = fs.ReadFile(os.DirFS(outDir), "API.md")
	if err != nil {
		t.Fatal(err)
	}
	want := "`testSDKEvent` SSE<br>Named events: `ready`, `reset`, `error` (`testSSEError`)"
	if !strings.Contains(string(content), want) {
		t.Errorf("API.md does not contain %q:\n%s", want, content)
	}
}

func TestDocRegistryGenerateTSValidate(t *testing.T) {
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
		text := string(content)
		for _, want := range []string{
			"type ValidatorInput = unknown;",
			"export function validatetestSDKEvent(raw: ValidatorInput): testSDKEvent",
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("validate.gen.ts does not contain %q:\n%s", want, text)
			}
		}
	})
}
