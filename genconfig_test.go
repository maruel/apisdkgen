// Tests for SDK type discovery, recursive layouts, and declaration names.

package apisdkgen

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/maruel/apisdkgen/apispec"
)

type testRecursiveSlice []testRecursiveSlice
type testRecursiveMap map[string]testRecursiveMap
type testRecursiveNode struct {
	Next *testRecursiveNode `json:"next,omitempty"`
}
type testTreeChildren []testTree
type testTree struct {
	Children testTreeChildren `json:"children"`
}
type testNamedMap map[string]string
type testContainerRoot struct {
	Values []testNamedMap `json:"values"`
}
type testIgnoredCycle struct {
	Name    string             `json:"name"`
	Ignored testRecursiveSlice `json:"-"`
	hidden  testRecursiveMap
}
type testSpecialCycle struct {
	Value testRecursiveSlice `json:"value"`
}

// ClientScope deliberately shares a generated name with apispec.ClientScope.
type ClientScope struct {
	Value string `json:"value"`
}

// APIClient deliberately shares the TypeScript client type's name.
type APIClient struct {
	Value string `json:"value"`
}

// JSONValue deliberately shares the Swift runtime type's name.
type JSONValue struct {
	Value string `json:"value"`
}

// Response deliberately shares a TypeScript HTTP global's name.
type Response struct {
	Value string `json:"value"`
}

// ValidatorInput deliberately shares a validator helper's name.
type ValidatorInput struct {
	Value string `json:"value"`
}
type testDocAuthKindSerializer struct {
	Value string `json:"value"`
}

type testJSONDiamondLeaf struct {
	Value int `json:"value"`
}
type testJSONDiamondLeft struct{ testJSONDiamondLeaf }
type testJSONDiamondRight struct{ testJSONDiamondLeaf }
type testJSONDiamond struct {
	testJSONDiamondLeft
	testJSONDiamondRight
	Name string `json:"name"`
}
type testJSONRecovered struct {
	testJSONDiamond
	Value string `json:"value"`
}
type testJSONUntagged struct{ Value int }
type testJSONTagged struct {
	Tagged string `json:"Value"`
}
type testJSONTagWins struct {
	testJSONUntagged
	testJSONTagged
}

func TestSDKTypeDiscovery(t *testing.T) {
	t.Parallel()
	t.Run("valid", func(t *testing.T) {
		cases := []struct {
			name    string
			seed    reflect.Type
			target  string
			special bool
		}{
			{"recursive slice TypeScript", reflect.TypeFor[testRecursiveSlice](), "TypeScript", false},
			{"recursive map TypeScript", reflect.TypeFor[testRecursiveMap](), "TypeScript", false},
			{"recursive node TypeScript", reflect.TypeFor[testRecursiveNode](), "TypeScript", false},
			{"recursive node Kotlin", reflect.TypeFor[testRecursiveNode](), "Kotlin", false},
			{"container-indirected tree Kotlin", reflect.TypeFor[testTree](), "Kotlin", false},
			{"container-indirected tree Swift", reflect.TypeFor[testTree](), "Swift", false},
			{"nested named map TypeScript", reflect.TypeFor[testContainerRoot](), "TypeScript", false},
			{"nested named map Kotlin", reflect.TypeFor[testContainerRoot](), "Kotlin", false},
			{"nested named map Swift", reflect.TypeFor[testContainerRoot](), "Swift", false},
			{"ignored cycles Swift", reflect.TypeFor[testIgnoredCycle](), "Swift", false},
			{"special boundary Swift", reflect.TypeFor[testSpecialCycle](), "Swift", true},
			{"TypeScript client name allowed in Kotlin", reflect.TypeFor[APIClient](), "Kotlin", false},
			{"Swift runtime name allowed in TypeScript", reflect.TypeFor[JSONValue](), "TypeScript", false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				dir := filepath.Join(t.TempDir(), "sdk")
				cfg := discoveryConfig(tc.seed)
				if tc.special {
					cfg.SpecialTypes = []apispec.SpecialType{{Type: reflect.TypeFor[testRecursiveSlice](), TSType: "string", KTType: "String", SwiftType: "String", DocType: "string"}}
				}
				api := newDiscoveryAPI(tc.target, dir, cfg)
				if err := api.Generate(); err != nil {
					t.Fatal(err)
				}
				first := discoveryFiles(t, dir)
				if err := api.Generate(); err != nil {
					t.Fatal(err)
				}
				if err := api.Verify(); err != nil {
					t.Fatal(err)
				}
				if diff := cmp.Diff(first, discoveryFiles(t, dir)); diff != "" {
					t.Fatalf("nondeterministic output (-first +second):\n%s", diff)
				}
			})
		}
		for _, target := range []string{"TypeScript", "Kotlin", "Swift"} {
			t.Run("compile "+target, func(t *testing.T) {
				dir := filepath.Join(t.TempDir(), "sdk")
				var seeds []reflect.Type
				special := false
				for _, tc := range cases {
					if tc.target == target {
						seeds = append(seeds, tc.seed)
						special = special || tc.special
					}
				}
				cfg := discoveryConfig(seeds[0])
				cfg.ExtraSeeds = append(cfg.ExtraSeeds, seeds[1:]...)
				if special {
					cfg.SpecialTypes = []apispec.SpecialType{{Type: reflect.TypeFor[testRecursiveSlice](), TSType: "string", KTType: "String", SwiftType: "String", DocType: "string"}}
				}
				api := newDiscoveryAPI(target, dir, cfg)
				if err := api.Generate(); err != nil {
					t.Fatal(err)
				}
				compileDiscovery(t, target, dir)
			})
		}
	})
	t.Run("error", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			seed   reflect.Type
			target string
			extra  reflect.Type
			want   string
		}{
			{"recursive slice Kotlin", reflect.TypeFor[testRecursiveSlice](), "Kotlin", nil, "version v1: invalid SDK types: Kotlin does not support recursive container alias apisdkgen.testRecursiveSlice"},
			{"recursive map Swift", reflect.TypeFor[testRecursiveMap](), "Swift", nil, "version v1: invalid SDK types: Swift does not support recursive container alias apisdkgen.testRecursiveMap"},
			{"inline node Swift", reflect.TypeFor[testRecursiveNode](), "Swift", nil, "version v1: invalid SDK types: unsupported Swift inline recursive struct apisdkgen.testRecursiveNode"},
			{"reflected name collision", reflect.TypeFor[ClientScope](), "TypeScript", reflect.TypeFor[apispec.ClientScope](), "version v1: invalid SDK types: generated type name \"ClientScope\" is shared by apisdkgen.ClientScope and apispec.ClientScope"},
			{"TypeScript HTTP global", reflect.TypeFor[Response](), "TypeScript", nil, "version v1: invalid SDK types: TypeScript type name \"Response\" conflicts with a generated declaration"},
			{"TypeScript runtime name", reflect.TypeFor[APIClient](), "TypeScript", nil, "version v1: invalid SDK types: TypeScript type name \"APIClient\" conflicts with a generated declaration"},
			{"Swift runtime name", reflect.TypeFor[JSONValue](), "Swift", nil, "version v1: invalid SDK types: generated Swift type name \"JSONValue\" conflicts with a generated declaration"},
			{"Kotlin serializer name", reflect.TypeFor[testDocAuthKindSerializer](), "Kotlin", nil, "version v1: invalid SDK types: generated Kotlin type name \"testDocAuthKindSerializer\" conflicts with a generated declaration"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				dir := filepath.Join(t.TempDir(), "sdk")
				cfg := discoveryConfig(tc.seed)
				if tc.extra != nil {
					cfg.ExtraSeeds = append(cfg.ExtraSeeds, tc.extra)
					cfg.SDKPackagePaths[tc.extra.PkgPath()] = struct{}{}
				}
				api := newDiscoveryAPI(tc.target, dir, cfg)
				if err := api.Generate(); err == nil || err.Error() != tc.want {
					t.Fatalf("Generate() = %v; want %q", err, tc.want)
				}
				if _, err := os.Stat(dir); !os.IsNotExist(err) {
					t.Fatalf("invalid discovery created outputs: %v", err)
				}
			})
		}
	})
	t.Run("unconfigured recursive container", func(t *testing.T) {
		cfg := discoveryConfig(reflect.TypeFor[testRecursiveMap]())
		cfg.SDKPackagePaths = nil
		api := newDiscoveryAPI("TypeScript", filepath.Join(t.TempDir(), "sdk"), cfg)
		const want = "version v1: invalid SDK types: recursive container apisdkgen.testRecursiveMap must belong to an SDK package or have a SpecialType mapping"
		if err := api.Generate(); err == nil || err.Error() != want {
			t.Fatalf("Generate() = %v; want %q", err, want)
		}
	})
	t.Run("validator helper name", func(t *testing.T) {
		cfg := discoveryConfig(reflect.TypeFor[ValidatorInput]())
		cfg.Routes[0].IsSSE = true
		api := newDiscoveryAPI("TypeScript", filepath.Join(t.TempDir(), "sdk"), cfg)
		const want = "version v1: invalid SDK types: TypeScript type name \"ValidatorInput\" conflicts with a generated declaration"
		if err := api.Generate(); err == nil || err.Error() != want {
			t.Fatalf("Generate() = %v; want %q", err, want)
		}
	})
	t.Run("generated enum members", func(t *testing.T) {
		for _, tc := range []struct {
			name, target string
			codes        []apispec.ErrorCodeSpec[testGeneratedErrorCode]
			want         string
		}{
			{"nonASCII symbol", "TypeScript", []apispec.ErrorCodeSpec[testGeneratedErrorCode]{{Code: "AÉ", Status: 400}}, `version v1: invalid SDK types: invalid TypeScript value name "testGeneratedErrorCodeAé"`},
			{"Kotlin catchall", "Kotlin", []apispec.ErrorCodeSpec[testGeneratedErrorCode]{{Code: "OTHER", Status: 400}}, "version v1: invalid SDK types: generated Kotlin enum member \"testGeneratedErrorCode.Other\" conflicts with a generated declaration"},
			{"TypeScript constant name", "TypeScript", []apispec.ErrorCodeSpec[testGeneratedErrorCode]{{Code: "A_B", Status: 400}, {Code: "A__B", Status: 401}}, "version v1: invalid SDK types: TypeScript value name \"testGeneratedErrorCodeAB\" is duplicated"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				cfg := discoveryConfig(reflect.TypeFor[testSDKEvent]())
				cfg.ErrorCodes = tc.codes
				api := newDiscoveryAPI(tc.target, filepath.Join(t.TempDir(), "sdk"), cfg)
				if err := api.Generate(); err == nil || err.Error() != tc.want {
					t.Fatalf("Generate() = %v; want %q", err, tc.want)
				}
			})
		}
	})
}

func TestJSONFieldDominance(t *testing.T) {
	t.Parallel()
	cfg := discoveryConfig(reflect.TypeFor[testJSONDiamond]())
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"diamond", testJSONDiamond{Name: "visible"}},
		{"ignored and unexported fields", testIgnoredCycle{Name: "visible", hidden: testRecursiveMap{}}},
		{"conflict then stronger candidate", testJSONRecovered{Value: "selected"}},
		{"tagged wins", testJSONTagWins{testJSONUntagged{Value: 42}, testJSONTagged{Tagged: "selected"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			var object map[string]json.RawMessage
			if err := json.Unmarshal(data, &object); err != nil {
				t.Fatal(err)
			}
			d := docRegistry[testGeneratedErrorCode]{cfg: &cfg}
			fields, err := jsonFields(d.cfg, reflect.TypeOf(tc.value))
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]struct{}{}
			for _, field := range fields {
				if field.field.Type.Kind() != reflect.String {
					t.Fatalf("selected field %s.%s has type %s; visible JSON fields are strings", field.owner, field.field.Name, field.field.Type)
				}
				name, _, _ := strings.Cut(field.field.Tag.Get("json"), ",")
				if name == "" {
					name = field.field.Name
				}
				got[name] = struct{}{}
			}
			want := map[string]struct{}{}
			for name := range object {
				want[name] = struct{}{}
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Fatalf("field visibility (-encoding/json +SDK):\n%s", diff)
			}
		})
	}
}

func discoveryConfig(seed reflect.Type) apispec.Config[testGeneratedErrorCode] {
	return apispec.Config[testGeneratedErrorCode]{
		SDKPackagePaths: map[string]struct{}{seed.PkgPath(): {}},
		Routes:          []apispec.Route{{Name: "get", Method: "GET", Path: "/", Resp: seed}},
		ExtraSeeds:      []reflect.Type{reflect.TypeFor[testGeneratedErrorResponse]()},
		ErrorModel:      apispec.ClientErrorModel{TypeName: "testGeneratedErrorResponse", TSDetailsExpr: "undefined", KTDetailsExpr: "null"},
		ErrorCodes:      []apispec.ErrorCodeSpec[testGeneratedErrorCode]{{Code: testGeneratedCodeBadRequest, Status: 400}, {Code: testGeneratedCodeUnknown, Status: 500}},
		KotlinPackage:   "fixture",
	}
}
func newDiscoveryAPI(target, dir string, cfg apispec.Config[testGeneratedErrorCode]) API {
	api := NewAPI(dir, "FixtureSDK", NewVersion("v1", ".", cfg))
	api.Languages = []Language{Language(target)}
	return api
}
func discoveryFiles(t *testing.T, dir string) map[string]string {
	files := map[string]string{}
	if err := fs.WalkDir(os.DirFS(dir), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(os.DirFS(dir), path)
		if err != nil {
			return err
		}
		files[path] = string(data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files
}
func compileDiscovery(t *testing.T, target, dir string) {
	var cmd *exec.Cmd
	switch target {
	case "TypeScript":
		dir = filepath.Join(dir, "ts", "v1")
		cmd = exec.CommandContext(t.Context(), "tsc", "--ignoreConfig", "--strict", "--noEmit", "--target", "ES2022", "types.gen.ts", "api.gen.ts")
	case "Kotlin":
		dir = filepath.Join(dir, "kotlin", "src", "main", "kotlin", "fixture")
		cp := os.Getenv("APISDKGEN_KOTLIN_CLASSPATH")
		if cp == "" {
			t.Skip("run make test to configure Kotlin dependencies")
		}
		// #nosec G204 G702 -- Compiler dependencies are explicit local test inputs; no shell is used.
		cmd = exec.CommandContext(t.Context(), "kotlinc", "-cp", cp, "-Xplugin="+os.Getenv("APISDKGEN_KOTLIN_SERIALIZATION_PLUGIN"), "Types.kt", "-d", "types.jar")
	case "Swift":
		dir = filepath.Join(dir, "swift", "Sources", "FixtureSDKV1")
		if _, err := exec.LookPath("swiftc"); err != nil {
			t.Skip("run make test to configure Swift")
		}
		cmd = exec.CommandContext(t.Context(), "swiftc", "-swift-version", "5", "-parse-as-library", "-emit-library", "Types.swift", "-o", "types.so")
	}
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s failed: %v\n%s", cmd.Path, err, out)
	}
}
