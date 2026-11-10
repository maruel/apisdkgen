// Tests for SDK output generation methods.

package apisdkgen

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/maruel/apisdkgen/apispec"
	"github.com/maruel/apisdkgen/testdata/mcpschematest"
)

type testNamedBytes []byte

type testAlwaysNonzeroList []int

func (testAlwaysNonzeroList) IsZero() bool { return false }

type testJSONKeys struct {
	Proto       *string            `json:"__proto__,omitempty"`
	Constructor *string            `json:"constructor,omitempty"`
	Values      map[string]*string `json:"values"`
	Fixed       [1]string          `json:"fixed,omitempty"`
	Empty       [0]string          `json:"empty,omitempty"`
}

type testJSONWire struct {
	Required    *string               `json:"required"`
	List        []int                 `json:"list"`
	Bytes       []byte                `json:"bytes"`
	NamedBytes  testNamedBytes        `json:"namedBytes"`
	Raw         json.RawMessage       `json:"raw"`
	Lookup      map[string]*int       `json:"lookup"`
	Omitted     *string               `json:"omitted,omitempty"`
	Container   *map[string]int       `json:"container,omitempty"`
	PointerZero *int                  `json:"pointerZero,omitzero"`
	ZeroList    testAlwaysNonzeroList `json:"zeroList,omitzero"`
	RawOptional json.RawMessage       `json:"rawOptional,omitempty"`
	C           string                `json:"c"`
	Decoder     string                `json:"decoder"`
	Encoder     string                `json:"encoder"`
	CodingKeys  string                `json:"CodingKeys"`
	Tree        testRecursiveTree     `json:"tree"`
}

// TestJSONWireContract compiles and runs generated validation against Go's JSON output.
func TestJSONWireContract(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	typ := reflect.TypeFor[testJSONWire]()
	cfg := apispec.Config[string]{SDKPackagePaths: map[string]struct{}{typ.PkgPath(): {}}, SpecialTypes: []apispec.SpecialType{{Type: reflect.TypeFor[json.RawMessage](), TSType: "unknown", KTType: "JsonElement", SwiftType: "JSONValue", DocType: "JSONValue", TSValidate: "%[1]s"}, {Type: reflect.TypeFor[[1]string](), TSType: "string[]", DocType: "string[]", TSValidate: "(%[1]s as string[])"}, {Type: reflect.TypeFor[[0]string](), TSType: "string[]", DocType: "string[]", TSValidate: "(%[1]s as string[])"}}, Routes: []apispec.Route{{Name: "events", Path: "/events", Resp: typ, IsSSE: true}, {Name: "keys", Path: "/keys", Resp: reflect.TypeFor[testJSONKeys](), IsSSE: true}}}
	api := NewAPI(dir, "WireSDK", NewVersion("v1", ".", cfg))
	api.Languages = []Language{TypeScript}
	if err := api.Generate(); err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(dir, "ts", "v1")
	var nilMap map[string]int
	zero := 0
	payload, err := json.Marshal(testJSONWire{PointerZero: &zero, Lookup: map[string]*int{"nil": nil}, Bytes: []byte{0, 1, 255}, Container: &nilMap, RawOptional: json.RawMessage("null"), C: "c", Decoder: "decoder", Encoder: "encoder", CodingKeys: "keys", Tree: testRecursiveTree{Children: []testRecursiveTree{{Children: []testRecursiveTree{{}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	emptyMap := map[string]int{}
	emptyPayload, err := json.Marshal(testJSONWire{List: []int{}, Bytes: []byte{}, NamedBytes: testNamedBytes{}, Lookup: map[string]*int{}, Container: &emptyMap, ZeroList: testAlwaysNonzeroList{}})
	if err != nil {
		t.Fatal(err)
	}
	proto := "special"
	keysPayload, err := json.Marshal(testJSONKeys{Proto: &proto, Values: map[string]*string{"__proto__": nil, "constructor": nil}})
	if err != nil {
		t.Fatal(err)
	}
	source := fmt.Sprintf(`import {validatetestJSONWire, validatetestJSONKeys} from "./validate.gen";
import type {testJSONWire} from "./types.gen";
const data: testJSONWire = JSON.parse(%q) as testJSONWire;
const parsed = validatetestJSONWire(data);
const empty = validatetestJSONWire(JSON.parse(%q));
if (empty.list?.length !== 0 || empty.bytes !== "" || empty.namedBytes !== "" || Object.keys(empty.lookup ?? {x:1}).length !== 0 || empty.container === null || Object.keys(empty.container ?? {x:1}).length !== 0 || empty.zeroList?.length !== 0 || "rawOptional" in empty) throw new Error("empty values changed");
if (parsed.required !== null || parsed.list !== null || parsed.bytes !== "AAH/" || parsed.raw !== null || parsed.lookup?.nil !== null || parsed.container !== null || parsed.zeroList !== null || parsed.rawOptional !== null || parsed.c !== "c" || parsed.tree.children?.[0]?.children?.length !== 1 || parsed.pointerZero !== 0 || parsed.omitted !== undefined || "omitted" in parsed) throw new Error("wire values changed");
let rejected = false;
try { validatetestJSONWire({...data, omitted: null}); } catch { rejected = true; }
if (!rejected) throw new Error("invalid optional null accepted");
const {raw: _raw, ...withoutRaw} = data;
rejected = false;
try { validatetestJSONWire(withoutRaw); } catch { rejected = true; }
if (!rejected) throw new Error("missing required raw value accepted");
const keys = validatetestJSONKeys(JSON.parse(%q));
if (!Object.prototype.hasOwnProperty.call(keys, "__proto__") || keys.__proto__ !== "special" || Object.prototype.hasOwnProperty.call(keys, "constructor") || keys.values === null || !Object.prototype.hasOwnProperty.call(keys.values, "__proto__") || keys.values.__proto__ !== null || keys.values.constructor !== null || keys.fixed[0] !== "" || Object.prototype.hasOwnProperty.call(keys, "empty")) throw new Error("special JSON keys changed");
rejected = false; try { validatetestJSONKeys({values:{}}); } catch { rejected = true; }
if (!rejected) throw new Error("nonempty fixed array incorrectly optional");
const {required: _required, ...withoutRequired} = data;
// @ts-expect-error required must be present even when every other field is supplied
const missing: testJSONWire = withoutRequired;
void missing;
rejected = false; try { validatetestJSONWire(withoutRequired); } catch { rejected = true; }
if (!rejected) throw new Error("missing required pointer accepted");
`, payload, emptyPayload, keysPayload)
	if err := os.WriteFile(filepath.Join(dir, "consumer.ts"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	runtimeDir := t.TempDir()
	// #nosec G204 -- Fixed compiler reads only generated files in private directories.
	cmd := exec.CommandContext(t.Context(), "tsc", "--ignoreConfig", "--strict", "--target", "ES2022", "--module", "commonjs", "--outDir", runtimeDir, "consumer.ts")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tsc: %v\n%s", err, out)
	}
	// #nosec G204 G702 -- The script is generated in the test's private directory.
	cmd = exec.CommandContext(t.Context(), "node", filepath.Join(runtimeDir, "consumer.js"))
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
}

type testDiscriminator struct {
	Kind string  `json:"kind"`
	Text *string `json:"text,omitempty"`
}

func TestDiscriminatorWireContract(t *testing.T) {
	t.Parallel()
	typ := reflect.TypeFor[testDiscriminator]()
	cfg := apispec.Config[string]{SDKPackagePaths: map[string]struct{}{typ.PkgPath(): {}}, Discriminated: []string{typ.Name()}, Routes: []apispec.Route{{Name: "events", Path: "/events", Resp: typ, IsSSE: true}}}
	dir := t.TempDir()
	api := NewAPI(dir, "WireSDK", NewVersion("v1", ".", cfg))
	api.Languages = []Language{TypeScript}
	if err := api.Generate(); err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(dir, "ts", "v1")
	empty := ""
	payload, err := json.Marshal(testDiscriminator{Kind: "text", Text: &empty})
	if err != nil {
		t.Fatal(err)
	}
	source := fmt.Sprintf(`import {validatetestDiscriminator} from "./validate.gen";
const parsed = validatetestDiscriminator(%s);
if (parsed.kind !== "text" || parsed.text !== "") throw new Error("empty payload lost");
for (const value of [{kind:"text"}, {kind:"text",text:null}]) {
 let failed = false; try { validatetestDiscriminator(value); } catch { failed = true; }
 if (!failed) throw new Error("invalid variant accepted");
}
if (validatetestDiscriminator({kind:"future"}).kind !== "future") throw new Error("unknown kind rejected");`, payload)
	if err := os.WriteFile(filepath.Join(dir, "consumer.ts"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	runtimeDir := t.TempDir()
	// #nosec G204 -- Fixed compiler reads generated test data in a private directory.
	compile := exec.CommandContext(t.Context(), "tsc", "--ignoreConfig", "--strict", "--target", "ES2022", "--module", "commonjs", "--outDir", runtimeDir, "consumer.ts")
	compile.Dir = dir
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("tsc: %v\n%s", err, out)
	}
	// #nosec G204 G702 -- Execute the generated private test script.
	run := exec.CommandContext(t.Context(), "node", filepath.Join(runtimeDir, "consumer.js"))
	run.Dir = dir
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
}

func TestEnumDiscriminatorWireContract(t *testing.T) {
	t.Parallel()
	typ := reflect.TypeFor[testEnvelope]()
	cfg := apispec.Config[string]{SDKPackagePaths: map[string]struct{}{typ.PkgPath(): {}}, Discriminated: []string{typ.Name()}, Routes: []apispec.Route{{Name: "events", Path: "/events", Resp: typ, IsSSE: true}}}
	d := &docRegistry[string]{cfg: &cfg, aliases: []aliasInfo{{name: "testEnvelopeKind", constants: []aliasConstant{{name: "testEnvelopeKindText", value: "text"}}}}, aliasNames: map[string]struct{}{"testEnvelopeKind": {}}}
	out := &preparedOutput{files: map[string]string{}}
	dir := t.TempDir()
	if err := d.generateTSTypes(dir, out); err != nil {
		t.Fatal(err)
	}
	if err := d.generateTSValidate(dir, out); err != nil {
		t.Fatal(err)
	}
	for path, content := range out.files {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	label := ""
	payload, err := json.Marshal(testEnvelope{Kind: "text", Label: &label, Source: testEnvelopeSource{Agent: "a"}, Text: &testEnvelopePayload{Text: ""}})
	if err != nil {
		t.Fatal(err)
	}
	source := fmt.Sprintf(`import {validatetestEnvelope} from "./validate.gen";
const valid=validatetestEnvelope(JSON.parse(%q));
if (valid.kind !== "text" || valid.text?.text !== "" || valid.label !== "" || valid.source?.agent !== "a") throw new Error("enum metadata lost");
let failed=false;try { validatetestEnvelope({kind:"text",label:""}); } catch { failed=true; }
if (!failed) throw new Error("missing variant accepted");
const unknown=validatetestEnvelope({kind:"future",label:"retained"});
if ((unknown.kind as string) !== "future" || unknown.label !== "retained") throw new Error("unknown metadata lost");`, payload)
	if err := os.WriteFile(filepath.Join(dir, "consumer.ts"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	runtimeDir := t.TempDir()
	// #nosec G204 -- Fixed compiler consumes test-generated sources in a private directory.
	compile := exec.CommandContext(t.Context(), "tsc", "--ignoreConfig", "--strict", "--target", "ES2022", "--module", "commonjs", "--outDir", runtimeDir, "consumer.ts")
	compile.Dir = dir
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("tsc: %v\n%s", err, output)
	}
	// #nosec G204 G702 -- Script is generated in the private test directory.
	run := exec.CommandContext(t.Context(), "node", filepath.Join(runtimeDir, "consumer.js"))
	run.Dir = dir
	if output, err := run.CombinedOutput(); err != nil {
		t.Fatalf("node: %v\n%s", err, output)
	}
}

// TestNativeJSONWireContract verifies native JSON encoding and decoding against Go output.
func TestNativeJSONWireContract(t *testing.T) {
	t.Parallel()
	typ := reflect.TypeFor[testJSONWire]()
	var nilMap map[string]int
	zero := 0
	payload, err := json.Marshal(testJSONWire{PointerZero: &zero, Lookup: map[string]*int{"nil": nil}, Bytes: []byte{0, 1, 255}, Container: &nilMap, RawOptional: json.RawMessage("null"), C: "c", Decoder: "decoder", Encoder: "encoder", CodingKeys: "keys", Tree: testRecursiveTree{Children: []testRecursiveTree{{Children: []testRecursiveTree{{}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	emptyMap := map[string]int{}
	emptyPayload, err := json.Marshal(testJSONWire{List: []int{}, Bytes: []byte{}, NamedBytes: testNamedBytes{}, Lookup: map[string]*int{}, Container: &emptyMap, ZeroList: testAlwaysNonzeroList{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range []Language{Kotlin, Swift} {
		t.Run(string(lang), func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			cfg := apispec.Config[string]{SDKPackagePaths: map[string]struct{}{typ.PkgPath(): {}}, KotlinPackage: "wirecheck", SpecialTypes: []apispec.SpecialType{{Type: reflect.TypeFor[json.RawMessage](), TSType: "unknown", KTType: "JsonElement", SwiftType: "JSONValue", DocType: "JSONValue", TSValidate: "%[1]s"}}, Routes: []apispec.Route{{Name: "events", Path: "/events", Resp: typ, IsSSE: true}}}
			api := NewAPI(dir, "WireSDK", NewVersion("v1", ".", cfg))
			api.Languages = []Language{lang}
			if err := api.Generate(); err != nil {
				t.Fatal(err)
			}
			payloadFile := filepath.Join(dir, "payload.json")
			if err := os.WriteFile(payloadFile, payload, 0600); err != nil {
				t.Fatal(err)
			}
			emptyFile := filepath.Join(dir, "empty.json")
			if err := os.WriteFile(emptyFile, emptyPayload, 0600); err != nil {
				t.Fatal(err)
			}
			var cmds []*exec.Cmd
			if lang == Kotlin {
				cp := os.Getenv("APISDKGEN_KOTLIN_CLASSPATH")
				if cp == "" {
					t.Skip("Kotlin runtime dependencies unavailable")
				}
				dir = filepath.Join(dir, "kotlin", "src", "main", "kotlin", "wirecheck")
				source := `package wirecheck
import kotlinx.serialization.encodeToString
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
fun main(args: Array<String>) {
 val json = Json { ignoreUnknownKeys = true; encodeDefaults = true }
 val payload = java.io.File(args[0]).readText()
 val decoded = json.decodeFromString<testJSONWire>(payload)
 val empty = json.decodeFromString<testJSONWire>(java.io.File(args[1]).readText())
 check(empty.list?.isEmpty() == true && empty.bytes?.isEmpty() == true && empty.namedBytes?.isEmpty() == true && empty.lookup?.isEmpty() == true && empty.container == JSONPresence.Value(emptyMap<String, Int>()) && empty.zeroList == JSONPresence.Value(emptyList<Int>()) && empty.rawOptional == JSONPresence.Missing)
 check(decoded.required == null && decoded.list == null && decoded.bytes?.contentEquals(byteArrayOf(0,1,-1)) == true && decoded.lookup?.get("nil") == null && decoded.container == JSONPresence.Null && decoded.zeroList == JSONPresence.Null && decoded.rawOptional == JSONPresence.Null && decoded.c == "c" && decoded.tree.children?.get(0)?.children?.size == 1 && decoded.pointerZero == 0)
 val encoded = json.parseToJsonElement(json.encodeToString(decoded)).jsonObject
 check(encoded.containsKey("required") && encoded["required"].toString() == "null" && encoded["bytes"].toString() == "\"AAH/\"" && encoded["container"].toString() == "null" && encoded["zeroList"].toString() == "null" && encoded["rawOptional"].toString() == "null" && !encoded.containsKey("omitted"))
 val missing = json.decodeFromString<testJSONWire>("""{"required":null,"list":null,"bytes":null,"namedBytes":null,"raw":null,"lookup":null,"c":"c","decoder":"decoder","encoder":"encoder","CodingKeys":"keys","tree":{}}""")
 check(missing.container == JSONPresence.Missing)
 val populated = missing.copy(container=JSONPresence.Value(mapOf("x" to 2)))
 check(json.decodeFromString<testJSONWire>(json.encodeToString(populated)).container == JSONPresence.Value(mapOf("x" to 2)))
 check(runCatching { json.decodeFromString<testJSONWire>("""{"list":null,"bytes":null,"namedBytes":null,"raw":null,"lookup":null,"c":"c","decoder":"decoder","encoder":"encoder","CodingKeys":"keys","tree":{}}""") }.isFailure)
}
`
				if err := os.WriteFile(filepath.Join(dir, "WireTest.kt"), []byte(source), 0600); err != nil {
					t.Fatal(err)
				}
				// #nosec G204 G702 -- Fixed compiler and runtime consume generated sources in a private directory.
				cmds = []*exec.Cmd{exec.CommandContext(t.Context(), "kotlinc", "-cp", cp, "-Xplugin="+os.Getenv("APISDKGEN_KOTLIN_SERIALIZATION_PLUGIN"), "Types.kt", "WireTest.kt", "-include-runtime", "-d", "wire.jar"), exec.CommandContext(t.Context(), "java", "-cp", "wire.jar"+string(os.PathListSeparator)+cp, "wirecheck.WireTestKt", payloadFile, emptyFile)}
			} else {
				if _, err := exec.LookPath("swiftc"); err != nil {
					t.Skip("Swift compiler unavailable")
				}
				dir = filepath.Join(dir, "swift", "Sources", "WireSDKV1")
				source := `import Foundation
@main struct WireTest {
 static func main() throws {
  let decoder = JSONDecoder(); let encoder = JSONEncoder()
  let decoded = try decoder.decode(testJSONWire.self, from: Data(contentsOf: URL(fileURLWithPath: CommandLine.arguments[1])))
  let empty = try decoder.decode(testJSONWire.self, from: Data(contentsOf: URL(fileURLWithPath: CommandLine.arguments[2])))
  guard empty.list?.isEmpty == true && empty.bytes?.isEmpty == true && empty.namedBytes?.isEmpty == true && empty.lookup?.isEmpty == true, case .value(let map) = empty.container, map.isEmpty, case .value(let list) = empty.zeroList, list.isEmpty, case .missing = empty.rawOptional else { fatalError("empty contract") }
  guard decoded.required == nil && decoded.list == nil && decoded.bytes == Data([0,1,255]) && decoded.pointerZero == 0 && decoded.c == "c" && decoded.decoder == "decoder" && decoded.encoder == "encoder" && decoded.CodingKeys == "keys" && decoded.tree.children?.first?.children?.count == 1, case .null = decoded.container, case .null = decoded.zeroList, case .null = decoded.rawOptional else { fatalError("null contract") }
  let encoded = try JSONSerialization.jsonObject(with: encoder.encode(decoded)) as! [String: Any]
  guard encoded["required"] is NSNull && encoded["container"] is NSNull && encoded["zeroList"] is NSNull && encoded["rawOptional"] is NSNull && encoded["c"] as? String == "c" && encoded["omitted"] == nil else { fatalError("encode contract") }
  let missing = try decoder.decode(testJSONWire.self, from: Data(#"{"required":null,"list":null,"bytes":null,"namedBytes":null,"raw":null,"lookup":null,"c":"c","decoder":"decoder","encoder":"encoder","CodingKeys":"keys","tree":{}}"#.utf8))
  guard case .missing = missing.container else { fatalError("missing contract") }
  let populated = testJSONWire(required: nil, list: nil, bytes: nil, namedBytes: nil, raw: nil, lookup: nil, container: .value(["x": 2]), c: "c", decoder: "decoder", encoder: "encoder", CodingKeys: "keys", tree: testRecursiveTree())
  let roundTrip = try decoder.decode(testJSONWire.self, from: encoder.encode(populated))
  guard case .value(let value) = roundTrip.container, value["x"] == 2 else { fatalError("value contract") }
  do { _ = try decoder.decode(testJSONWire.self, from: Data(#"{"list":null,"bytes":null,"namedBytes":null,"raw":null,"lookup":null,"c":"c","decoder":"decoder","encoder":"encoder","CodingKeys":"keys","tree":{}}"#.utf8)); fatalError("missing required accepted") } catch {}
 }
}
`
				if err := os.WriteFile(filepath.Join(dir, "WireTest.swift"), []byte(source), 0600); err != nil {
					t.Fatal(err)
				}
				// #nosec G204 G702 -- Fixed compiler and runtime consume generated sources in a private directory.
				cmds = []*exec.Cmd{exec.CommandContext(t.Context(), "swiftc", "-parse-as-library", "Types.swift", "WireTest.swift", "-o", "wire"), exec.CommandContext(t.Context(), filepath.Join(dir, "wire"), payloadFile, emptyFile)}
			}
			for _, cmd := range cmds {
				cmd.Dir = dir
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%s: %v\n%s", cmd.Path, err, out)
				}
			}
		})
	}
}

type State string
type testAuthKind string

type testSDKEvent struct {
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
}

type testRootList []int
type testRootMap map[string]int
type testRootBytes []byte
type testRootNestedBytes [][]byte
type testRootBytesMap map[string][]byte

type testQueryIDs []string

type testQueryScalar struct{ Value string }
type testStructOnlyQuery struct {
	Since testQueryScalar `json:"since,omitempty" query:"since"` //nolint:modernize // Query omitempty is meaningful for mapped structs.
}

type testQueryInput struct {
	Path  string        `json:"-" path:"id"`
	Limit *int          `json:"limit" query:"limit"`
	Only  *bool         `json:"unread" query:"unread_only"`
	Text  *string       `json:"text" query:"q"`
	IDs   *testQueryIDs `json:"ids" query:"ids"`
	Maybe *string       `json:"maybe,omitempty" query:"maybe"`
	Extra string        `json:"extra"`
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
	testJSONSameName //nolint:govet // Deliberately tests an encoding/json name collision.

	Name string `json:"name"`
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
		{name: "string boolean map", typ: reflect.TypeFor[map[string]bool](), want: "Record<string, boolean> | null"},
		{name: "string value map", typ: reflect.TypeFor[map[string]string](), want: "Record<string, string> | null"},
		{name: "raw JSON map", typ: reflect.TypeFor[map[string]json.RawMessage](), want: "Record<string, JSONValue | null> | null"},
		{name: "any map", typ: reflect.TypeFor[map[string]any](), want: "Record<string, JSONValue> | null"},
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
	cfg := apispec.Config[string]{
		SDKPackagePaths: map[string]struct{}{reflect.TypeFor[State]().PkgPath(): {}},
		ExtraSeeds:      []reflect.Type{reflect.TypeFor[State](), reflect.TypeFor[testAuthKind]()},
	}

	t.Run("string constants", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name   string
			source string
			want   []aliasConstant
		}{
			{"string min and max", `const (StateFirst = min(State("first"), State("last")); StateLast = max(StateFirst, State("last")))`, []aliasConstant{{name: "StateFirst", value: "first"}, {name: "StateLast", value: "last"}}},
			{"mixed and nested builtins", `const (prefix = min("middle", "first"); StateFirst = min("last", (State)(prefix)); StateLast = max("last", min(StateFirst, State("middle"))))`, []aliasConstant{{name: "StateFirst", value: "first"}, {name: "StateLast", value: "last"}}},
			{"variadic builtin type retention", `const StateFirst = min("z", State("m"), "a")`, []aliasConstant{{name: "StateFirst", value: "a"}}},
			{"single builtin argument", `const StateFirst = max(State("first"))`, []aliasConstant{{name: "StateFirst", value: "first"}}},
			{"unrelated builtin results", `const (StateFirst State = "first"; IsFirst = min(StateFirst, State("last")) == "first"; Length = len(StateFirst); Small = min(1, 2); Sum = real(complex(1, 2)) + imag(complex(3, 4)))`, []aliasConstant{{name: "StateFirst", value: "first"}}},
			{"parenthesized type", `const StateFirst (State) = "first"`, []aliasConstant{{name: "StateFirst", value: "first"}}},
			{"parenthesized conversion", `const StateFirst = (State)("first")`, []aliasConstant{{name: "StateFirst", value: "first"}}},
			{"unrelated boolean comparison", `const (StateFirst State = "first"; IsFirst = StateFirst == "first"; IsLater = StateFirst > "a")`, []aliasConstant{{name: "StateFirst", value: "first"}}},
			{"typed and inherited", `const (StateFirst State = "first"; StateAgain; Other = "untyped"; OtherAgain)`, []aliasConstant{{name: "StateFirst", value: "first"}, {name: "StateAgain", value: "first"}}},
			{"multiple values", `const (StateFirst, StateSecond State = "first", "second"; StateThird, StateFourth)`, []aliasConstant{{name: "StateFirst", value: "first"}, {name: "StateSecond", value: "second"}, {name: "StateThird", value: "first"}, {name: "StateFourth", value: "second"}}},
			{"inferred references and expressions", `const (StateFirst = State("first"); StateAgain = StateFirst; StateLast = StatePrefix + State("last"); StatePrefix = State("prefix-"))`, []aliasConstant{{name: "StateFirst", value: "first"}, {name: "StateAgain", value: "first"}, {name: "StateLast", value: "prefix-last"}, {name: "StatePrefix", value: "prefix-"}}},
			{"string helper and parenthesized conversion", `const (prefix string = "prefix-"; StateFirst = (State(prefix + "first")); Other = string(StateFirst))`, []aliasConstant{{name: "StateFirst", value: "prefix-first"}}},
			{"block reset and unrelated numeric constants", `const StateFirst State = "first"; const Other = 42; const OtherText = "untyped"`, []aliasConstant{{name: "StateFirst", value: "first"}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "types.go"), []byte("package fixture\ntype State string\n"+tc.source+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				docs, err := loadDocsInDir(dir, &cfg)
				if err != nil {
					t.Fatal(err)
				}
				if diff := cmp.Diff(tc.want, docs.aliases[0].constants, cmp.AllowUnexported(aliasConstant{})); diff != "" {
					t.Fatalf("constants (-want +got):\n%s", diff)
				}
			})
		}
	})
	t.Run("parenthesized alias declaration", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "types.go"), []byte("package fixture\ntype State (string)\nconst StateFirst State = \"first\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		docs, err := loadDocsInDir(dir, &cfg)
		if err != nil {
			t.Fatal(err)
		}
		if len(docs.aliases) != 1 {
			t.Fatalf("aliases = %v; want State", docs.aliases)
		}
		if diff := cmp.Diff([]aliasConstant{{name: "StateFirst", value: "first"}}, docs.aliases[0].constants, cmp.AllowUnexported(aliasConstant{})); diff != "" {
			t.Fatalf("constants (-want +got):\n%s", diff)
		}
	})
	t.Run("unsupported string constant", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct{ name, declaration, want string }{
			{"plain", `const StateFirst State = external.Value`, "string constant StateFirst: unsupported expression *ast.SelectorExpr"},
			{"inferred builtin imported argument", `const StateFirst = min(State("first"), external.Value)`, "string constant StateFirst: unsupported expression *ast.SelectorExpr"},
			{"inferred builtin numeric conversion", `const StateFirst = max(State("first"), State(65))`, "string constant StateFirst: unsupported expression *ast.BasicLit"},
			{"parenthesized type", `const StateFirst (State) = external.Value`, "string constant StateFirst: unsupported expression *ast.SelectorExpr"},
			{"parenthesized conversion", `const StateFirst = (State)(external.Value)`, "string constant StateFirst: unsupported expression *ast.SelectorExpr"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "types.go"), []byte("package fixture\ntype State string\n"+tc.declaration+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				_, err := loadDocsInDir(dir, &cfg)
				if err == nil || err.Error() != tc.want {
					t.Fatalf("loadDocsInDir() = %v", err)
				}
			})
		}
	})

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
		docs, err := loadDocsInDir(dir, &cfg)
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
				if got, want := b.String(), "export interface testTSTaggedRequest {\n  IDs: string | null;\n  name: string;\n}\n"; got != want {
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

			outDir := "generated"
			out := &preparedOutput{files: map[string]string{}}
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
			if err := docs.generateTS(outDir, out); err != nil {
				t.Fatal(err)
			}
			content := out.files[filepath.Join(outDir, "api.gen.ts")]
			assertGolden(t, "api.gen.ts", content)

			if err := docs.generateMarkdownDoc(filepath.Join(outDir, "API.md"), out); err != nil {
				t.Fatal(err)
			}
			content = out.files[filepath.Join(outDir, "API.md")]
			assertGolden(t, "API.md", content)
		})
	})
	t.Run("generateMarkdownDoc", func(t *testing.T) {
		t.Parallel()

		t.Run("enum fields retain their type and values", func(t *testing.T) {
			t.Parallel()
			outDir := "generated"
			out := &preparedOutput{files: map[string]string{}}
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
			if err := docs.generateMarkdownDoc(filepath.Join(outDir, "API.md"), out); err != nil {
				t.Fatal(err)
			}
			data := out.files[filepath.Join(outDir, "API.md")]
			assertGolden(t, "API.md", data)
		})
	})
	t.Run("writeKotlinClient", func(t *testing.T) { //nolint:dupl // Kotlin and Swift clients need parallel SSE golden coverage.
		t.Run("query routes", func(t *testing.T) {
			d := &docRegistry[string]{cfg: &apispec.Config[string]{
				KotlinPackage: "example.api",
				Routes: []apispec.Route{
					{Name: "events", Path: "/items/{id}/events", Resp: reflect.TypeFor[testSDKEvent](), IsSSE: true, QueryParams: []string{"cursor"}},
					{Name: "rawEvents", Path: "/raw-events", Resp: reflect.TypeFor[testSDKEvent](), IsSSE: true},
				},
			}}
			dir := "generated"
			out := &preparedOutput{files: map[string]string{}}
			if err := d.writeKotlinClient(dir, out); err != nil {
				t.Fatal(err)
			}
			data := out.files[filepath.Join(dir, "ApiClient.kt")]
			assertGolden(t, "ApiClient.kt", data)
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
			dir := "generated"
			out := &preparedOutput{files: map[string]string{}}
			if err := d.writeKotlinClient(dir, out); err != nil {
				t.Fatal(err)
			}
			data := out.files[filepath.Join(dir, "ApiClient.kt")]
			assertGolden(t, "ApiClient.kt", data)
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

		t.Run("with SSE routes writes validator", func(t *testing.T) {
			t.Parallel()

			outDir := "generated"
			out := &preparedOutput{files: map[string]string{}}
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

			if err := docs.generateTSValidate(outDir, out); err != nil {
				t.Fatal(err)
			}
			content := out.files[filepath.Join(outDir, "validate.gen.ts")]
			assertGolden(t, "validate.gen.ts", content)
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
			dir := "generated"
			out := &preparedOutput{files: map[string]string{}}
			if err := d.generateTSValidate(dir, out); err != nil {
				t.Fatal(err)
			}
			data := out.files[filepath.Join(dir, "validate.gen.ts")]
			assertGolden(t, "validate.gen.ts", data)
		})
		t.Run("discriminated without enum", func(t *testing.T) {
			t.Parallel()
			d := &docRegistry[string]{cfg: &apispec.Config[string]{
				Routes:          []apispec.Route{{Name: "events", Resp: reflect.TypeFor[testEnvelope](), IsSSE: true}},
				Discriminated:   []string{"testEnvelope"},
				SDKPackagePaths: map[string]struct{}{reflect.TypeFor[testEnvelope]().PkgPath(): {}},
			}}
			dir := "generated"
			out := &preparedOutput{files: map[string]string{}}
			if err := d.generateTSValidate(dir, out); err != nil {
				t.Fatal(err)
			}
			data := out.files[filepath.Join(dir, "validate.gen.ts")]
			assertGolden(t, "validate.gen.ts", data)
		})
	})
	t.Run("writeSwiftClient", func(t *testing.T) { //nolint:dupl // Kotlin and Swift clients need parallel SSE golden coverage.
		t.Run("query routes", func(t *testing.T) {
			d := &docRegistry[string]{cfg: &apispec.Config[string]{
				KotlinPackage: "example.api",
				Routes: []apispec.Route{
					{Name: "events", Path: "/items/{id}/events", Resp: reflect.TypeFor[testSDKEvent](), IsSSE: true, QueryParams: []string{"cursor"}},
					{Name: "rawEvents", Path: "/raw-events", Resp: reflect.TypeFor[testSDKEvent](), IsSSE: true},
				},
			}}
			dir := "generated"
			out := &preparedOutput{files: map[string]string{}}
			if err := d.writeSwiftClient(dir, out); err != nil {
				t.Fatal(err)
			}
			data := out.files[filepath.Join(dir, "ApiClient.swift")]
			assertGolden(t, "ApiClient.swift", data)
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
			dir := "generated"
			out := &preparedOutput{files: map[string]string{}}
			if err := d.writeSwiftClient(dir, out); err != nil {
				t.Fatal(err)
			}
			data := out.files[filepath.Join(dir, "ApiClient.swift")]
			assertGolden(t, "ApiClient.swift", data)
		})
	})
}

func TestGenerate(t *testing.T) {
	t.Parallel()
	t.Run("reachable aliases", func(t *testing.T) {
		t.Parallel()
		source := t.TempDir()
		if err := os.WriteFile(filepath.Join(source, "types.go"), []byte(`package fixture
type State string
const StateReady State = "ready"
type testAuthKind string
const testAuthKindToken testAuthKind = "token"
type Unused string
const UnusedBroken Unused = external.Value
type É string
`), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, lang := range []Language{Kotlin, Swift, TypeScript} {
			t.Run(string(lang), func(t *testing.T) {
				t.Parallel()
				cfg := discoveryConfig(reflect.TypeFor[State]())
				cfg.ExtraSeeds = append(cfg.ExtraSeeds, reflect.TypeFor[testAuthKind]())
				root := filepath.Join(t.TempDir(), "sdk")
				api := NewAPI(root, "FixtureSDK", NewVersion("v1", source, cfg))
				api.Languages = []Language{lang}
				if err := api.Generate(); err != nil {
					t.Fatal(err)
				}
				if lang == TypeScript {
					data, err := fs.ReadFile(os.DirFS(root), "ts/v1/types.gen.ts")
					if err != nil {
						t.Fatal(err)
					}
					assertGolden(t, "types.gen.ts", string(data))
				}
				compileDiscovery(t, string(lang), root)
			})
		}
	})
	t.Run("file endings", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name   string
			routes []apispec.Route
		}{
			{name: "empty"},
			{name: "JSON", routes: []apispec.Route{{Name: "getEvent", Method: "GET", Path: "/event", Resp: reflect.TypeFor[testSDKEvent]()}}},
			{name: "SSE", routes: []apispec.Route{{Name: "events", Method: "GET", Path: "/events", Resp: reflect.TypeFor[testSDKEvent](), IsSSE: true}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "stale"), []byte("old output"), 0o600); err != nil {
					t.Fatal(err)
				}
				api := NewAPI(dir, "FixtureSDK", NewVersion("v1", ".", apispec.Config[string]{
					KotlinPackage:   "fixture.api",
					Routes:          tc.routes,
					SDKPackagePaths: map[string]struct{}{reflect.TypeFor[testSDKEvent]().PkgPath(): {}},
				}))
				if err := api.Generate(); err != nil {
					t.Fatal(err)
				}
				names := []string{"API.v1.md", "kotlin/src/main/kotlin/fixture/api/ApiClient.kt", "swift/Sources/FixtureSDKV1/ApiClient.swift", "kotlin/src/main/kotlin/fixture/api/Types.kt", "swift/Sources/FixtureSDKV1/Types.swift", "ts/v1/api.gen.ts", "ts/v1/types.gen.ts"}
				if tc.name == "SSE" {
					names = append(names, "ts/v1/validate.gen.ts")
				}
				for _, name := range names {
					data, err := fs.ReadFile(os.DirFS(dir), name)
					if err != nil {
						t.Fatal(err)
					}
					text := string(data)
					if !strings.HasSuffix(text, "\n") || strings.HasSuffix(text, "\n\n") {
						t.Errorf("%s must end with one newline", name)
					}
				}
				if err := api.Verify(); err != nil {
					t.Fatal(err)
				}
			})
		}
	})
	t.Run("configured error code type", func(t *testing.T) {
		t.Parallel()

		errorResponseType := reflect.TypeFor[testGeneratedErrorResponse]()
		cfg := apispec.Config[testGeneratedErrorCode]{
			Routes: []apispec.Route{{
				Name: "getEvent", Method: "GET", Path: "/event", Resp: reflect.TypeFor[testSDKEvent](),
			}},
			SDKPackagePaths: map[string]struct{}{errorResponseType.PkgPath(): {}},
			ExtraSeeds:      []reflect.Type{errorResponseType},
			KotlinPackage:   "fixture.api",
			ErrorModel: apispec.ClientErrorModel{
				TypeName:      errorResponseType.Name(),
				TSDetailsExpr: "undefined",
				KTDetailsExpr: "null",
			},
			ErrorCodes: []apispec.ErrorCodeSpec[testGeneratedErrorCode]{
				{Code: testGeneratedCodeBadRequest, Status: 400},
				{Code: testGeneratedCodeUnknown, Status: 500},
			},
		}
		root := t.TempDir()
		tsDir := filepath.Join(root, "ts", "v1")
		kotlinDir := filepath.Join(root, "kotlin", "src", "main", "kotlin", "fixture", "api")
		swiftDir := filepath.Join(root, "swift", "Sources", "FixtureSDKV1")
		api := NewAPI(root, "FixtureSDK", NewVersion("v1", ".", cfg))
		if err := api.Generate(); err != nil {
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
		t.Run("compile", func(t *testing.T) {
			t.Run("TypeScript", func(t *testing.T) {
				cmd := exec.CommandContext(t.Context(), "tsc", "--ignoreConfig", "--noEmit", "--strict", "--noUnusedLocals", "--noUnusedParameters", "--target", "ES2022", "--module", "ESNext", "--moduleResolution", "bundler", "api.gen.ts", "types.gen.ts")
				cmd.Dir = tsDir
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("TypeScript fixture compilation failed: %v\n%s", err, out)
				}
			})
			t.Run("Kotlin", func(t *testing.T) {
				cp := os.Getenv("APISDKGEN_KOTLIN_CLASSPATH")
				if cp == "" {
					t.Skip("run make test to configure Kotlin dependencies")
				}
				plugin := os.Getenv("APISDKGEN_KOTLIN_SERIALIZATION_PLUGIN")
				if plugin == "" {
					t.Fatal("APISDKGEN_KOTLIN_SERIALIZATION_PLUGIN is required")
				}
				// #nosec G204 G702 -- Compiler paths are generated temporary files and explicitly configured local dependencies; no shell is used.
				cmd := exec.CommandContext(t.Context(), "kotlinc", "-cp", cp, "-Xplugin="+plugin, "ApiClient.kt", "Types.kt", "-d", "fixture.jar")
				cmd.Dir = kotlinDir
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("Kotlin fixture compilation failed: %v\n%s", err, out)
				}
			})
			t.Run("Swift", func(t *testing.T) {
				if _, err := exec.LookPath("swiftc"); err != nil {
					t.Skip("Swift compiler is optional")
				}
				args := []string{"-swift-version", "5", "-parse-as-library", "-emit-library", "ApiClient.swift", "Types.swift", "-o", "fixture.so"}
				if runtime.GOOS == "linux" {
					transport, err := filepath.Abs("testdata/SSETransport.swift")
					if err != nil {
						t.Fatal(err)
					}
					args = append(args, transport)
				}
				// #nosec G204 -- Fixed compiler flags consume generated source and the repository's Linux transport fixture.
				cmd := exec.CommandContext(t.Context(), "swiftc", args...)
				cmd.Dir = swiftDir
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("Swift fixture compilation failed: %v\n%s", err, out)
				}
			})
		})
	})

	t.Run("TypeScript query headers", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cfg := apispec.Config[string]{
			SDKPackagePaths: map[string]struct{}{reflect.TypeFor[testSDKEvent]().PkgPath(): {}},
			ExtraSeeds:      []reflect.Type{reflect.TypeFor[testGeneratedErrorResponse]()},
			ErrorModel:      apispec.ClientErrorModel{TypeName: "testGeneratedErrorResponse", TSDetailsExpr: "undefined"},
			Routes:          []apispec.Route{{Name: "list", Method: "GET", Path: "/items", Query: reflect.TypeFor[testEmbeddedQueryRequest](), Resp: reflect.TypeFor[testSDKEvent](), HeadersArg: true}},
		}
		api := NewAPI(dir, "FixtureSDK", NewVersion("v1", ".", cfg))
		api.Languages = []Language{TypeScript}
		dir = filepath.Join(dir, "ts", "v1")
		if err := api.Generate(); err != nil {
			t.Fatal(err)
		}
		consumer := `import { createApiClient } from "./api.gen";
async function main() {
  let calls = 0;
  const client = createApiClient(async (url, init) => {
    const headers = new Headers(init?.headers);
    if (url !== "/items?limit=3" || init?.method !== "GET" || init?.body !== undefined || headers.get("X-Test") !== "expected") throw new Error("query request contract failed");
    calls++;
    return new Response(JSON.stringify({kind: "listed"}));
  });
  const response = await client.list({limit: 3}, {"X-Test": "expected"});
  if (response.kind !== "listed" || calls !== 1) throw new Error("query response contract failed");
}
void main();
`
		if err := os.WriteFile(filepath.Join(dir, "consumer.ts"), []byte(consumer), 0o600); err != nil {
			t.Fatal(err)
		}
		runtimeDir := t.TempDir()
		for _, cmd := range []*exec.Cmd{
			// #nosec G204 G702 -- The compiler and flags are fixed; runtimeDir is an isolated test output directory.
			exec.CommandContext(t.Context(), "tsc", "--ignoreConfig", "--strict", "--noUnusedLocals", "--noUnusedParameters", "--target", "ES2022", "--module", "commonjs", "--outDir", runtimeDir, "consumer.ts"),
			// #nosec G204 G702 -- Execute the compiled test fixture without a shell.
			exec.CommandContext(t.Context(), "node", filepath.Join(runtimeDir, "consumer.js")),
		} {
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s failed: %v\n%s", cmd.Path, err, out)
			}
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
				api := NewAPI(dir, "FixtureSDK", NewVersion("v1", ".", cfg))
				api.Languages = []Language{TypeScript}
				dir = filepath.Join(dir, "ts", "v1")
				if err := api.Generate(); err != nil {
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
				cmd := exec.CommandContext(t.Context(), "tsc", "--ignoreConfig", "--noEmit", "--strict", "--noUnusedLocals", "--noUnusedParameters", "--target", "ES2022", "--module", "ESNext", "--moduleResolution", "bundler", "consumer.ts")
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
					{Name: "error", Handler: "onControlError"},
					{Name: "failure", Handler: "onFailure", Resp: reflect.TypeFor[testSSEError]()},
				}},
				{Name: "rootEvents", Path: "/events", Resp: eventType, IsSSE: true, QueryParams: []string{"cursor"}},
				{Name: "rawEvents", Path: "/raw-events", Resp: eventType, IsSSE: true},
			},
		}
		api := NewAPI(dir, "FixtureSDK", NewVersion("v1", ".", cfg))
		api.Languages = []Language{TypeScript}
		dir = filepath.Join(dir, "ts", "v1")
		if err := api.Generate(); err != nil {
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
let messages = 0, ready = 0, failures = 0, errors = 0, controlErrors = 0;
const controls: string[] = [];
const handlers = {
  onMessage: (event: { kind: string }) => { check(event.kind === "update"); messages++; controls.push("message"); },
  onCheckpoint: (event: { cursor: string }) => { check(event.cursor === "next"); controls.push("checkpoint"); },
  onStats: (event: { kind: string }) => { check(event.kind === "stats"); controls.push("stats"); },
  onReset: (event: { bootstrapURL: string }) => { check(event.bootstrapURL === "/bootstrap"); controls.push("reset"); },
  onReady: () => { ready++; },
  onControlError: () => { controlErrors++; },
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
source.dispatchEvent(new MessageEvent("ready", { data: "" }));
source.dispatchEvent(new MessageEvent("failure", { data: JSON.stringify({message: "expired"}) }));
source.dispatchEvent(new MessageEvent("message", { data: "not JSON" }));
check(messages === 1 && ready === 1 && failures === 1 && errors === 1);
check(controls.join(",") === "checkpoint,message,stats,reset");
source.dispatchEvent(new Event("error"));
check(controlErrors === 0 && errors === 1);
source.dispatchEvent(new MessageEvent("error", { data: "" }));
check(controlErrors === 1 && errors === 1);
source.dispatchEvent(new MessageEvent("error", { data: 42 }));
check(controlErrors === 1);
const callbackFailure = new Error("callback failure");
const callbackErrors: unknown[] = [];
const callbacks = client.workspace(value).events(value, value, "", {
  ...handlers,
  onMessage: () => { throw callbackFailure; },
  onCheckpoint: () => { throw callbackFailure; },
  onReady: () => { throw callbackFailure; },
  onControlError: () => { throw callbackFailure; },
  onError: (error: unknown) => { callbackErrors.push(error); },
});
callbacks.dispatchEvent(new MessageEvent("message", { data: JSON.stringify({kind: "update"}) }));
callbacks.dispatchEvent(new MessageEvent("checkpoint", { data: JSON.stringify({cursor: "next"}) }));
callbacks.dispatchEvent(new MessageEvent("ready", { data: "" }));
callbacks.dispatchEvent(new Event("error"));
check(callbackErrors.length === 3);
callbacks.dispatchEvent(new MessageEvent("error", { data: "" }));
check(callbackErrors.length === 4 && callbackErrors.every(error => error === callbackFailure));
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
		runtimeDir := t.TempDir()
		for _, cmd := range []*exec.Cmd{
			// #nosec G204 G702 -- The compiler and flags are fixed; runtimeDir is an isolated test output directory.
			exec.CommandContext(t.Context(), "tsc", "--ignoreConfig", "--strict", "--target", "ES2022", "--module", "commonjs", "--outDir", runtimeDir, "consumer.ts"),
			// #nosec G204 G702 -- Execute the compiled test fixture without a shell.
			exec.CommandContext(t.Context(), "node", filepath.Join(runtimeDir, "consumer.js")),
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
			}}, {Name: "plainEvents", Path: "/events", Resp: eventType, IsSSE: true, QueryParams: []string{"cursor"}}, {Name: "getEvent", Method: "GET", Path: "/event", Resp: eventType}},
		}
		api := NewAPI(dir, "FixtureSDK", NewVersion("v1", ".", cfg))
		api.Languages = []Language{Kotlin}
		dir = filepath.Join(dir, "kotlin", "src", "main", "kotlin", cfg.KotlinPackage)
		if err := api.Generate(); err != nil {
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
			}}, {Name: "plainEvents", Path: "/events", Resp: eventType, IsSSE: true, QueryParams: []string{"cursor"}}},
		}
		api := NewAPI(dir, "FixtureSDK", NewVersion("v1", ".", cfg))
		api.Languages = []Language{Swift}
		dir = filepath.Join(dir, "swift", "Sources", "FixtureSDKV1")
		if err := api.Generate(); err != nil {
			t.Fatal(err)
		}
		transport, err := filepath.Abs("testdata/SSETransport.swift")
		if err != nil {
			t.Fatal(err)
		}
		// Compile the fixture in the client's source scope to verify private policy
		// without widening the generated SDK's visibility for tests.
		clientSource, err := fs.ReadFile(os.DirFS(dir), "ApiClient.swift")
		if err != nil {
			t.Fatal(err)
		}
		fixtureSource, err := fs.ReadFile(os.DirFS("."), "testdata/SSEStreamTest.swift")
		if err != nil {
			t.Fatal(err)
		}
		combined := filepath.Join(dir, "Runtime.swift")
		// #nosec G703 -- This source file is inside the test's private temporary directory.
		if err := os.WriteFile(combined, append(append(clientSource, '\n'), fixtureSource...), 0o600); err != nil {
			t.Fatal(err)
		}

		binary := filepath.Join(dir, "runtime")
		// #nosec G204 -- Fixed compiler and flags consume only generated temporary files and the repository fixture.
		cmd := exec.CommandContext(t.Context(), "swiftc", "-swift-version", "5", "-parse-as-library", combined, filepath.Join(dir, "Types.swift"), transport, "-o", binary)
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
	t.Run("typed query module names", func(t *testing.T) {
		t.Parallel()
		type Foundation struct{}
		type CoreFoundation struct{}
		type Swift struct{}
		for _, typ := range []reflect.Type{reflect.TypeFor[Foundation](), reflect.TypeFor[CoreFoundation](), reflect.TypeFor[Swift]()} {
			t.Run(typ.Name(), func(t *testing.T) {
				t.Parallel()
				cfg := apispec.Config[string]{
					SDKPackagePaths: map[string]struct{}{typ.PkgPath(): {}},
					ExtraSeeds:      []reflect.Type{typ, reflect.TypeFor[testSSEError]()},
					ErrorModel:      apispec.ClientErrorModel{TypeName: "testSSEError"},
					Routes:          []apispec.Route{{Name: "list", Method: "GET", Path: "/items", Query: reflect.TypeFor[testEmbeddedQueryFields](), Resp: reflect.TypeFor[testSDKEvent]()}},
				}
				api := NewAPI(t.TempDir(), "FixtureSDK", NewVersion("v1", ".", cfg))
				api.Languages = []Language{Language("Swift")}
				want := `version v1: invalid SDK types: generated Swift type name "` + typ.Name() + `" conflicts with a generated declaration`
				if err := api.Generate(); err == nil || err.Error() != want {
					t.Fatalf("Generate() = %v; want %q", err, want)
				}
			})
		}
	})

	t.Run("typed query inputs", func(t *testing.T) {
		t.Parallel()
		for _, lang := range []Language{Kotlin, Swift, TypeScript} {
			t.Run(string(lang), func(t *testing.T) {
				t.Parallel()
				cfg := apispec.Config[string]{
					SDKPackagePaths: map[string]struct{}{reflect.TypeFor[testQueryInput]().PkgPath(): {}},
					KotlinPackage:   "querycheck",
					APIDocTitle:     "Typed query fixture",
					ExtraSeeds:      []reflect.Type{reflect.TypeFor[testSSEError]()},
					ErrorModel:      apispec.ClientErrorModel{TypeName: "testSSEError", TSCodeExpr: `"UNKNOWN"`, TSMessageExpr: "err.message", TSDetailsExpr: "undefined", KTCodeExpr: `"UNKNOWN"`, KTMessageExpr: "err.message", KTDetailsExpr: "null", SwiftCodeExpr: `"UNKNOWN"`, SwiftMessageExpr: "errResp.message", SwiftDetailsExpr: "nil"},
					SpecialTypes:    []apispec.SpecialType{{Type: reflect.TypeFor[testQueryIDs](), TSType: "string", KTType: "String", SwiftType: "String", DocType: "string"}, {Type: reflect.TypeFor[testQueryScalar](), TSType: "string", KTType: "String", SwiftType: "String", DocType: "string"}},
					Routes: []apispec.Route{
						{Name: "list", Method: "GET", Path: "/items/{queryPath}", Query: reflect.TypeFor[testQueryInput](), Resp: reflect.TypeFor[testSDKEvent]()},
						{Name: "update", Method: "POST", Path: "/items", Query: reflect.TypeFor[testQueryInput](), Req: reflect.TypeFor[testSDKEvent](), Resp: reflect.TypeFor[testSDKEvent](), HeadersArg: true},
						{Name: "reuse", Method: "POST", Path: "/reuse", Query: reflect.TypeFor[testQueryInput](), Req: reflect.TypeFor[testQueryInput](), Resp: reflect.TypeFor[testSDKEvent]()},
						{Name: "rootList", Method: "POST", Path: "/root-list", Req: reflect.TypeFor[testRootList](), Resp: reflect.TypeFor[testRootList]()},
						{Name: "rootMap", Method: "POST", Path: "/root-map", Req: reflect.TypeFor[testRootMap](), Resp: reflect.TypeFor[testRootMap]()},
						{Name: "rootBytes", Method: "POST", Path: "/root-bytes", Req: reflect.TypeFor[testRootBytes](), Resp: reflect.TypeFor[testRootBytes]()},
						{Name: "rootBytesHeaders", Method: "GET", Path: "/root-bytes-headers", Resp: reflect.TypeFor[testRootBytes](), HeadersArg: true},
						{Name: "queryOnlyEmpty", Method: "GET", Path: "/query-only-empty", Query: reflect.TypeFor[testStructOnlyQuery](), Resp: reflect.TypeFor[testSDKEvent]()},
						{Name: "queryOnlyValue", Method: "GET", Path: "/query-only-value", Query: reflect.TypeFor[testStructOnlyQuery](), Resp: reflect.TypeFor[testSDKEvent]()},
						{Name: "rootNestedBytes", Method: "POST", Path: "/root-nested", Req: reflect.TypeFor[testRootNestedBytes](), Resp: reflect.TypeFor[testRootNestedBytes]()},
						{Name: "rootBytesMap", Method: "POST", Path: "/root-bytes-map", Req: reflect.TypeFor[testRootBytesMap](), Resp: reflect.TypeFor[testRootBytesMap]()},
						{Name: "rootArray", Method: "GET", Path: "/root-array", Resp: reflect.TypeFor[testSDKEvent](), IsArray: true},
						{Name: "events", Path: "/events", Query: reflect.TypeFor[testQueryInput](), Resp: reflect.TypeFor[testSDKEvent](), IsSSE: true},
						{Name: "namedEvents", Path: "/events", Query: reflect.TypeFor[testQueryInput](), Resp: reflect.TypeFor[testSDKEvent](), IsSSE: true, SSEEvents: []apispec.SSEEvent{{Name: "ready", Handler: "onReady"}}},
					},
				}
				if lang == Kotlin {
					type Pair struct{}
					cfg.ExtraSeeds = append(cfg.ExtraSeeds, reflect.TypeFor[Pair]())
				}
				if lang == Swift {
					type NSNumber struct{}
					type JSONSerialization struct{}
					type NSNull struct{}
					type CharacterSet struct{}
					type CFGetTypeID struct{}
					type CFBooleanGetTypeID struct{}
					type Encodable struct{}
					cfg.ExtraSeeds = append(cfg.ExtraSeeds, reflect.TypeFor[NSNumber](), reflect.TypeFor[JSONSerialization](), reflect.TypeFor[NSNull](), reflect.TypeFor[CharacterSet](), reflect.TypeFor[CFGetTypeID](), reflect.TypeFor[CFBooleanGetTypeID](), reflect.TypeFor[Encodable]())
				}
				dir := t.TempDir()
				api := NewAPI(dir, "QuerySDK", NewVersion("v1", ".", cfg))
				api.Languages = []Language{lang}
				if err := api.Generate(); err != nil {
					t.Fatal(err)
				}
				if lang == TypeScript {
					doc, err := fs.ReadFile(os.DirFS(dir), "API.v1.md")
					if err != nil {
						t.Fatal(err)
					}
					assertGolden(t, "API.md", string(doc))
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasPrefix(r.URL.Path, "/root-") {
						if r.URL.Path == "/root-bytes-headers" {
							if r.Header.Get("X-Test") != "expected" {
								t.Error("missing header")
								http.Error(w, "bad header", http.StatusBadRequest)
								return
							}
							if err := json.NewEncoder(w).Encode(testRootBytes{0, 1, 255}); err != nil {
								t.Errorf("encode bytes: %v", err)
							}
							return
						}
						var value any
						switch r.URL.Path {
						case "/root-list":
							value = new(testRootList)
						case "/root-map":
							value = new(testRootMap)
						case "/root-bytes":
							value = new(testRootBytes)
						case "/root-nested":
							value = new(testRootNestedBytes)
						case "/root-bytes-map":
							value = new(testRootBytesMap)
						case "/root-array":
							value = new([]testSDKEvent)
						}
						if r.Method == http.MethodPost {
							if err := json.NewDecoder(r.Body).Decode(value); err != nil {
								t.Errorf("root body: %v", err)
								http.Error(w, "invalid", http.StatusBadRequest)
								return
							}
						}
						if err := json.NewEncoder(w).Encode(value); err != nil {
							t.Errorf("encode root: %v", err)
						}
						return
					}
					want := url.Values{"limit": {"0"}, "unread_only": {"false"}, "q": {"a +&雪"}, "ids": {"a,b"}}
					switch r.URL.Path {
					case "/query-only-empty":
						want = url.Values{}
					case "/query-only-value":
						want = url.Values{"since": {"x"}}
					case "/items/blank":
						want.Set("q", "")
					case "/items/empty":
						want = url.Values{}
					}
					if diff := cmp.Diff(want, r.URL.Query()); diff != "" || r.URL.ForceQuery {
						t.Errorf("%s query (-want +got): %s; trailing query delimiter: %v", r.URL.Path, diff, r.URL.ForceQuery)
						http.Error(w, "bad query", http.StatusBadRequest)
						return
					}
					switch {
					case r.URL.Path == "/reuse":
						var body map[string]any
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Errorf("decode reuse body: %v", err)
							http.Error(w, "bad body", http.StatusBadRequest)
							return
						}
						for _, key := range []string{"limit", "unread", "text", "ids"} {
							value, present := body[key]
							if !present || value != nil {
								t.Errorf("body field %s = %v, present %v", key, value, present)
								http.Error(w, "bad body", http.StatusBadRequest)
								return
							}
						}
						if body["extra"] != "body" {
							t.Errorf("bad reuse body: %v", body)
							http.Error(w, "bad body", http.StatusBadRequest)
							return
						}
					case r.Method == http.MethodPost:
						var body testSDKEvent
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Kind != "request" || r.Header.Get("X-Test") != "expected" {
							t.Errorf("body/headers: %v, %+v, %v", err, body, r.Header)
							http.Error(w, "bad body", http.StatusBadRequest)
							return
						}
					case r.ContentLength > 0:
						t.Error("GET query sent a JSON body")
					}
					if r.URL.Path == "/events" {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = w.Write([]byte("data: {\"kind\":\"listed\"}\n\n"))
					} else {
						_, _ = w.Write([]byte(`{"kind":"listed"}`))
					}
				}))
				t.Cleanup(server.Close)
				var cmds []*exec.Cmd
				switch lang {
				case TypeScript:
					dir = filepath.Join(dir, "ts", "v1")
					data, err := os.ReadFile("testdata/QueryClient.ts")
					if err != nil {
						t.Fatal(err)
					}
					// #nosec G703 -- Source is written only under this test's private generated output.
					if err := os.WriteFile(filepath.Join(dir, "consumer.ts"), data, 0o600); err != nil {
						t.Fatal(err)
					}
					runtimeDir := t.TempDir()
					// #nosec G204 G702 -- Fixed compilers and flags consume trusted fixtures and isolated test outputs without a shell.
					cmds = []*exec.Cmd{exec.CommandContext(t.Context(), "tsc", "--ignoreConfig", "--strict", "--target", "ES2022", "--module", "commonjs", "--outDir", runtimeDir, "consumer.ts"), exec.CommandContext(t.Context(), "node", filepath.Join(runtimeDir, "consumer.js"), server.URL)}
				case Kotlin:
					cp := os.Getenv("APISDKGEN_KOTLIN_CLASSPATH")
					if cp == "" {
						t.Skip("set APISDKGEN_KOTLIN_CLASSPATH for Kotlin runtime")
					}
					dir = filepath.Join(dir, "kotlin", "src", "main", "kotlin", cfg.KotlinPackage)
					fixture, err := filepath.Abs("testdata/QueryClient.kt")
					if err != nil {
						t.Fatal(err)
					}
					// #nosec G204 G702 -- Fixed compilers and flags consume trusted fixtures and isolated test outputs without a shell.
					cmds = []*exec.Cmd{exec.CommandContext(t.Context(), "kotlinc", "-cp", cp, "-Xplugin="+os.Getenv("APISDKGEN_KOTLIN_SERIALIZATION_PLUGIN"), "ApiClient.kt", "Types.kt", fixture, "-include-runtime", "-d", "runtime.jar"), exec.CommandContext(t.Context(), "java", "-cp", "runtime.jar"+string(os.PathListSeparator)+cp, "querycheck.QueryClientKt", server.URL)}
				case Swift:
					if runtime.GOOS != "linux" {
						t.Skip("query fixture supplies Linux byte transport")
					}
					if _, err := exec.LookPath("swiftc"); err != nil {
						t.Skip("Swift compiler is optional")
					}
					dir = filepath.Join(dir, "swift", "Sources", "QuerySDKV1")
					fixture, err := filepath.Abs("testdata/QueryClient.swift")
					if err != nil {
						t.Fatal(err)
					}
					// #nosec G204 G702 -- Fixed compilers and flags consume trusted fixtures and isolated test outputs without a shell.
					cmds = []*exec.Cmd{exec.CommandContext(t.Context(), "swiftc", "-swift-version", "5", "-parse-as-library", "ApiClient.swift", "Types.swift", fixture, "-o", "runtime"), exec.CommandContext(t.Context(), filepath.Join(dir, "runtime"), server.URL)}
				}
				for _, cmd := range cmds {
					cmd.Dir = dir
					if out, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("%s: %v\n%s", cmd.Path, err, out)
					}
				}
			})
		}
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

type testMetaIdentity struct {
	Name string `json:"name"`
}
type testCustomMeta struct {
	Kind     string `json:"kind"`
	Identity testMetaIdentity
	Extra    map[string]any
}

func (m testCustomMeta) MarshalJSON() ([]byte, error) {
	fields := make(map[string]any, len(m.Extra)+1)
	maps.Copy(fields, m.Extra)
	fields["identity"] = m.Identity
	fields["kind"] = m.Kind
	return json.Marshal(fields)
}

type testMetaEnvelope struct {
	Meta testCustomMeta `json:"meta"`
}

func TestCustomSourcesRoundTrip(t *testing.T) {
	t.Parallel()
	payload, err := json.Marshal(testMetaEnvelope{Meta: testCustomMeta{Kind: "event", Identity: testMetaIdentity{Name: "client"}, Extra: map[string]any{"future": map[string]any{"enabled": true}}}})
	if err != nil {
		t.Fatal(err)
	}
	sources := make(map[string]string)
	for _, lang := range []string{"ts", "kt", "swift"} {
		data, err := os.ReadFile("testdata/CustomMeta." + lang) //nolint:gosec // lang comes from the fixed fixture list.
		if err != nil {
			t.Fatal(err)
		}
		sources[lang] = string(data)
	}
	for _, lang := range []Language{TypeScript, Kotlin, Swift} {
		t.Run(string(lang), func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			typ := reflect.TypeFor[testMetaEnvelope]()
			cfg := apispec.Config[string]{SDKPackagePaths: map[string]struct{}{typ.PkgPath(): {}}, KotlinPackage: "customcheck", Discriminated: []string{"testCustomMeta"}, ExtraSeeds: []reflect.Type{reflect.TypeFor[testSSEError]()}, ErrorModel: apispec.ClientErrorModel{TypeName: "testSSEError", TSCodeExpr: `"UNKNOWN"`, TSMessageExpr: "err.message", TSDetailsExpr: "undefined", KTCodeExpr: `"UNKNOWN"`, KTMessageExpr: "err.message", KTDetailsExpr: "null", SwiftCodeExpr: `"UNKNOWN"`, SwiftMessageExpr: "errResp.message", SwiftDetailsExpr: "nil"}, SpecialTypes: []apispec.SpecialType{{
				Type: reflect.TypeFor[testCustomMeta](), TSType: "testCustomMeta", KTType: "testCustomMeta", SwiftType: "testCustomMeta", DocType: "testCustomMeta", TSSource: sources["ts"], KotlinSource: sources["kt"], SwiftSource: sources["swift"], TSValidator: "checkMeta", TSValidate: "checkMeta(%[1]s, %[2]q)", Dependencies: []reflect.Type{reflect.TypeFor[testMetaIdentity]()}, KotlinSymbols: []string{"MetaCodec"}, SwiftSymbols: []string{"MetaKey"},
			}}, Routes: []apispec.Route{{Name: "events", Path: "/events", Resp: typ, IsSSE: true, SSEEvents: []apispec.SSEEvent{{Name: "custom", Handler: "onCustom", Resp: reflect.TypeFor[testCustomMeta]()}}}, {Name: "customEvents", Path: "/custom", Resp: reflect.TypeFor[testCustomMeta](), IsSSE: true}}}
			api := NewAPI(root, "CustomSDK", NewVersion("v1", ".", cfg))
			api.Languages = []Language{lang}
			if err := api.Generate(); err != nil {
				t.Fatal(err)
			}
			payloadFile := filepath.Join(root, "payload.json")
			if err := os.WriteFile(payloadFile, payload, 0600); err != nil {
				t.Fatal(err)
			}
			var dir, sourceName, source string
			var cmds []*exec.Cmd
			switch lang {
			case TypeScript:
				dir = filepath.Join(root, "ts", "v1")
				if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"commonjs"}`), 0600); err != nil {
					t.Fatal(err)
				}
				sourceName = "consumer.ts"
				source = `import { validatetestMetaEnvelope, validatetestCustomMeta } from "./validate.gen";
import { readFileSync } from "node:fs";
const v = validatetestMetaEnvelope(JSON.parse(readFileSync(process.argv[2], "utf8")));
if (v.meta.identity.name !== "client" || (v.meta.future as {enabled: boolean}).enabled !== true) throw Error("decode");
const root = validatetestCustomMeta(v.meta);
if (root.kind !== "event" || root.identity.name !== "client" || (root.future as {enabled: boolean}).enabled !== true) throw Error("root");
const r = validatetestMetaEnvelope(JSON.parse(JSON.stringify(v)));
if ((r.meta.future as {enabled: boolean}).enabled !== true) throw Error("encode");
try { validatetestMetaEnvelope({meta: {kind: "event", identity: {name: 1}}}); throw Error("accepted invalid"); } catch (e) { if (!(e instanceof TypeError)) throw e; }`
				// Avoid dependency on Node's ambient type declarations in the temporary package.
				source = strings.ReplaceAll(source, `import { readFileSync } from "node:fs";`, `declare function require(name: string): { readFileSync(path: string, encoding: string): string }; declare const process: { argv: string[] }; const { readFileSync } = require("fs");`)
				// #nosec G204 -- Commands use fixed tools and the temporary fixture path.
				cmds = []*exec.Cmd{exec.CommandContext(t.Context(), "tsc", "--ignoreConfig", "--strict", "--target", "ES2022", "--module", "commonjs", "consumer.ts", "api.gen.ts"), exec.CommandContext(t.Context(), "node", "consumer.js", payloadFile)}
			case Kotlin:
				cp := os.Getenv("APISDKGEN_KOTLIN_CLASSPATH")
				if cp == "" {
					t.Skip("Kotlin runtime dependencies unavailable")
				}
				dir = filepath.Join(root, "kotlin", "src", "main", "kotlin", "customcheck")
				sourceName = "Runtime.kt"
				source = `package customcheck
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
fun main(args: Array<String>) {
 val json = Json { ignoreUnknownKeys = true }
 val v = json.decodeFromString<testMetaEnvelope>(java.io.File(args[0]).readText())
 check(v.meta.kind == "event" && v.meta.identity.name == "client" && v.meta.extra.getValue("future").jsonObject.getValue("enabled").jsonPrimitive.boolean)
 val r = json.decodeFromString<testMetaEnvelope>(json.encodeToString(v))
 check(r.meta.extra.getValue("future").jsonObject.getValue("enabled").jsonPrimitive.boolean)
 try { json.decodeFromString<testMetaEnvelope>("""{"meta":{"kind":"event","identity":{"name":[]}}}"""); error("accepted invalid") } catch (_: kotlinx.serialization.SerializationException) {}
 for (kind in listOf("123", "true", "null", "{}", "[]")) {
  try { json.decodeFromString<testMetaEnvelope>("""{"meta":{"kind":$kind,"identity":{"name":"client"}}}"""); error("accepted invalid kind $kind") } catch (_: kotlinx.serialization.SerializationException) {}
 }
}`
				// #nosec G204 G702 -- Compiler dependencies and sources are locally installed test inputs.
				cmds = []*exec.Cmd{exec.CommandContext(t.Context(), "kotlinc", "-cp", cp, "-Xplugin="+os.Getenv("APISDKGEN_KOTLIN_SERIALIZATION_PLUGIN"), "ApiClient.kt", "Types.kt", "CustomtestCustomMeta.kt", sourceName, "-include-runtime", "-d", "runtime.jar"), exec.CommandContext(t.Context(), "java", "-cp", "runtime.jar"+string(os.PathListSeparator)+cp, "customcheck.RuntimeKt", payloadFile)}
			case Swift:
				if _, err := exec.LookPath("swiftc"); err != nil {
					t.Skip("Swift compiler unavailable")
				}
				dir = filepath.Join(root, "swift", "Sources", "CustomSDKV1")
				transport, err := filepath.Abs("testdata/SSETransport.swift")
				if err != nil {
					t.Fatal(err)
				}
				sourceName = "Runtime.swift"
				source = `import Foundation
@main struct Run {
 static func main() throws {
  let decoder = JSONDecoder(); let encoder = JSONEncoder()
  let v = try decoder.decode(testMetaEnvelope.self, from: Data(contentsOf: URL(fileURLWithPath: CommandLine.arguments[1])))
  guard v.meta.kind == "event" && v.meta.identity.name == "client", case .object(let future) = v.meta.extra["future"], case .bool(true) = future["enabled"] else { fatalError("decode") }
  let r = try decoder.decode(testMetaEnvelope.self, from: encoder.encode(v))
  guard case .object(let extra) = r.meta.extra["future"], case .bool(true) = extra["enabled"] else { fatalError("encode") }
  do { _ = try decoder.decode(testMetaEnvelope.self, from: Data(#"{"meta":{"kind":"event","identity":{"name":[]}}}"#.utf8)); fatalError("accepted invalid") } catch {}
 }
}`
				// #nosec G204 -- Fixed compiler consumes test sources in a private temporary directory.
				cmds = []*exec.Cmd{exec.CommandContext(t.Context(), "swiftc", "-swift-version", "5", "-parse-as-library", "ApiClient.swift", "Types.swift", "CustomtestCustomMeta.swift", sourceName, transport, "-o", "runtime"), exec.CommandContext(t.Context(), filepath.Join(dir, "runtime"), payloadFile)}
			}
			if err := os.WriteFile(filepath.Join(dir, sourceName), []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			for _, cmd := range cmds {
				cmd.Dir = dir
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%s: %v\n%s", cmd.Path, err, out)
				}
			}
		})
	}
}

func TestMCPClientWireContract(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		lang     Language
		required bool
	}{{"TypeScript", TypeScript, false}, {"TypeScript renamed", TypeScript, true}, {"Kotlin presence", Kotlin, false}, {"Kotlin required", Kotlin, true}, {"Swift", Swift, false}} {
		t.Run(tc.name, func(t *testing.T) {
			lang := tc.lang
			t.Parallel()
			dir := t.TempDir()
			typ := reflect.TypeFor[mcpschematest.JSONRPCRequest]()
			cfg := apispec.Config[string]{SDKPackagePaths: map[string]struct{}{typ.PkgPath(): {}}, KotlinPackage: "mcpcheck", ExtraSeeds: []reflect.Type{reflect.TypeFor[mcpschematest.JSONRPCNotification](), reflect.TypeFor[mcpschematest.RequestMeta](), reflect.TypeFor[mcpschematest.ServerDiscoverResult]()},
				SpecialTypes: []apispec.SpecialType{{Type: reflect.TypeFor[json.RawMessage](), TSType: "unknown", KTType: "JsonElement", SwiftType: "JSONValue", DocType: "JSONValue", TSValidate: "%[1]s"}, {Type: reflect.TypeFor[any](), TSType: "unknown", KTType: "JsonElement", SwiftType: "JSONValue", DocType: "JSONValue", TSValidate: "%[1]s"}},
				ErrorModel:   apispec.ClientErrorModel{TypeName: "JSONRPCResponse", TSCodeExpr: `String(err.error?.code ?? "UNKNOWN")`, TSMessageExpr: `err.error?.message ?? ""`, TSDetailsExpr: "undefined", KTCodeExpr: `err.error?.code?.toString() ?: "UNKNOWN"`, KTMessageExpr: `err.error?.message ?: ""`, KTDetailsExpr: "null", SwiftCodeExpr: `String(errResp.error?.code ?? 0)`, SwiftMessageExpr: `errResp.error?.message ?? ""`, SwiftDetailsExpr: "nil"},
				Routes:       []apispec.Route{{Name: "mcp", Method: "POST", Path: "", Req: typ, Resp: reflect.TypeFor[mcpschematest.JSONRPCResponse](), HeadersArg: true}},
			}
			if tc.required {
				cfg.Routes[0].Req = reflect.TypeFor[mcpschematest.RequiredRequest]()
				cfg.Routes[0].Resp = reflect.TypeFor[mcpschematest.RequiredResponse]()
				cfg.ErrorModel.TypeName = "RequiredResponse"
			}
			api := NewAPI(dir, "MCPSDK", NewVersion("v1", "testdata/mcpschematest", cfg))
			api.Languages = []Language{lang}
			if err := api.Generate(); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/empty-stream" {
					body, err := io.ReadAll(r.Body)
					if err != nil || r.Method != http.MethodPost || len(body) != 0 {
						t.Errorf("empty stream request: method %s, body %q, err %v", r.Method, body, err)
						http.Error(w, "invalid request", http.StatusBadRequest)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					if _, err := fmt.Fprint(w, "data: empty-ok\n\n"); err != nil {
						t.Error(err)
						return
					}
					if err := http.NewResponseController(w).Flush(); err != nil {
						t.Error(err)
						return
					}
					<-r.Context().Done()
					return
				}
				if r.Header.Get("Mcp-Method") != "server/discover" || r.Header.Get("Mcp-Protocol-Version") != "2026-07-28" {
					t.Errorf("invalid headers: %v", r.Header)
					http.Error(w, "headers", http.StatusBadRequest)
					return
				}
				var request mcpschematest.JSONRPCRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Errorf("decode request: %v", err)
					http.Error(w, "invalid", http.StatusBadRequest)
					return
				}
				var params struct {
					Meta mcpschematest.RequestMeta `json:"_meta"`
				}
				if err := json.Unmarshal(request.Params, &params); err != nil || request.JSONRPC != "2.0" || request.Method != mcpschematest.MethodDiscovery || len(request.ID) == 0 || string(request.ID) == "null" || params.Meta.ProtocolVersion != "2026-07-28" || params.Meta.ClientInfo.Name == "" {
					t.Errorf("invalid request: %#v, params %#v, err %v", request, params, err)
					http.Error(w, "request", http.StatusBadRequest)
					return
				}
				if r.Header.Get("Accept") == "text/event-stream" {
					w.Header().Set("Content-Type", "text/event-stream")
					payload, err := json.Marshal(mcpschematest.JSONRPCNotification{JSONRPC: "2.0", Method: "notification"})
					if err != nil {
						t.Error(err)
						return
					}
					if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
						t.Error(err)
						return
					}
					if err := http.NewResponseController(w).Flush(); err != nil {
						t.Error(err)
						return
					}
					<-r.Context().Done()
					return
				}
				if err := json.NewEncoder(w).Encode(mcpschematest.JSONRPCResponse{JSONRPC: "2.0", ID: request.ID, Result: mcpschematest.ServerDiscoverResult{Instructions: "ready"}}); err != nil {
					t.Error(err)
				}
			}))
			t.Cleanup(server.Close)
			params, err := json.Marshal(struct {
				Meta mcpschematest.RequestMeta `json:"_meta"`
			}{Meta: mcpschematest.RequestMeta{ProtocolVersion: "2026-07-28", ClientInfo: mcpschematest.Implementation{Name: "swift", Version: "1"}}})
			if err != nil {
				t.Fatal(err)
			}
			request, err := json.Marshal(mcpschematest.JSONRPCRequest{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: mcpschematest.MethodDiscovery, Params: params})
			if err != nil {
				t.Fatal(err)
			}
			requestFile := filepath.Join(dir, "request.json")
			if err := os.WriteFile(requestFile, request, 0600); err != nil {
				t.Fatal(err)
			}
			var cmds []*exec.Cmd
			switch lang {
			case TypeScript:
				dir = filepath.Join(dir, "ts", "v1")
				source, err := os.ReadFile("testdata/MCPClient.ts")
				if err != nil {
					t.Fatal(err)
				}
				if tc.required {
					source = []byte(strings.ReplaceAll(string(source), "JSONRPCRequest", "RequiredRequest"))
				}
				// #nosec G703 -- Destination is inside the generated output in the test temporary directory.
				if err := os.WriteFile(filepath.Join(dir, "consumer.ts"), source, 0600); err != nil {
					t.Fatal(err)
				}
				output := t.TempDir()
				// #nosec G204 G702 -- Fixed compilers use isolated generated sources and fixture server.
				cmds = []*exec.Cmd{exec.CommandContext(t.Context(), "tsc", "--ignoreConfig", "--strict", "--target", "ES2022", "--module", "commonjs", "--outDir", output, "consumer.ts"), exec.CommandContext(t.Context(), "node", filepath.Join(output, "consumer.js"), server.URL, requestFile)}
			case Kotlin:
				cp := os.Getenv("APISDKGEN_KOTLIN_CLASSPATH")
				if cp == "" {
					t.Skip("Kotlin dependencies unavailable")
				}
				dir = filepath.Join(dir, "kotlin", "src", "main", "kotlin", "mcpcheck")
				source, err := os.ReadFile("testdata/MCPClient.kt")
				if err != nil {
					t.Fatal(err)
				}
				if tc.required {
					source = []byte(strings.ReplaceAll(string(source), "JSONRPCRequest", "RequiredRequest"))
					source = []byte(strings.ReplaceAll(string(source), `result is JSONPresence.Value && result.value.jsonObject`, `result != null && result.jsonObject`))
				}
				fixture := filepath.Join(dir, "MCPClient.kt")
				// #nosec G703 -- Fixture is written in this test's private generated output.
				if err := os.WriteFile(fixture, source, 0600); err != nil {
					t.Fatal(err)
				}
				// #nosec G204 G702 -- Locally installed compiler dependencies and fixed fixture paths are trusted test inputs.
				cmds = []*exec.Cmd{exec.CommandContext(t.Context(), "kotlinc", "-cp", cp, "-Xplugin="+os.Getenv("APISDKGEN_KOTLIN_SERIALIZATION_PLUGIN"), "ApiClient.kt", "Types.kt", fixture, "-include-runtime", "-d", "runtime.jar"), exec.CommandContext(t.Context(), "java", "-cp", "runtime.jar"+string(os.PathListSeparator)+cp, "mcpcheck.MCPClientKt", server.URL, requestFile)}
			case Swift:
				if runtime.GOOS != "linux" {
					t.Skip("fixture transport targets Linux")
				}
				if _, err := exec.LookPath("swiftc"); err != nil {
					t.Skip("Swift compiler unavailable")
				}
				dir = filepath.Join(dir, "swift", "Sources", "MCPSDKV1")
				fixture, err := filepath.Abs("testdata/MCPClient.swift")
				if err != nil {
					t.Fatal(err)
				}
				transport, err := filepath.Abs("testdata/SSETransport.swift")
				if err != nil {
					t.Fatal(err)
				}
				// #nosec G204 G702 -- Fixed compiler consumes generated sources and fixtures under test control.
				cmds = []*exec.Cmd{exec.CommandContext(t.Context(), "swiftc", "-swift-version", "5", "-parse-as-library", "ApiClient.swift", "Types.swift", fixture, transport, "-o", "runtime"), exec.CommandContext(t.Context(), filepath.Join(dir, "runtime"), server.URL, requestFile)}
			}
			for _, cmd := range cmds {
				cmd.Dir = dir
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%s: %v\n%s", cmd.Path, err, out)
				}
			}
		})
	}
}
