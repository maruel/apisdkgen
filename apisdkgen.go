// Package apisdkgen generates typed client SDKs and API references.
// It supports TypeScript, Kotlin, and Swift from Go DTO types and route definitions.
//
// SDK specifications are defined by each package in an exported, immutable
// SDKAPI variable of type apispec.Config with that API's error-code type. The
// consumer's gen-api-sdk command registers those specs and chooses output paths.
//
// Generated TypeScript packages export versioned subpaths and are private;
// consumers own publishing. Kotlin packages target the JVM with Java 21. Their
// stream method returns a cold flow of decoded SSE frames and does not reconnect.
// Swift packages expose a product per version for macOS 13 and iOS 16 or later
// and use Apple Foundation networking APIs.
package apisdkgen

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/maruel/apisdkgen/apispec"
)

type outputPaths struct {
	TypeScriptDir string
	KotlinDir     string
	SwiftDir      string
	MarkdownFile  string
}

// validate checks emitter requirements after Config.Validate succeeds.
func (o *outputPaths) validate[C ~string](cfg *apispec.Config[C]) error {
	if o.KotlinDir != "" {
		for part := range strings.SplitSeq(cfg.KotlinPackage, ".") {
			if !isIdentifier(part) {
				return fmt.Errorf("KotlinPackage %q must be a nonempty dotted package name", cfg.KotlinPackage)
			}
		}
	}
	names := map[string]struct{}{}
	if o.TypeScriptDir != "" {
		for _, scope := range cfg.ClientScopes {
			names["/"+scope.Name] = struct{}{}
		}
	}
	nativeNames := map[string]struct{}{}
	if o.KotlinDir != "" {
		nativeNames["stream"] = struct{}{}
	}
	if hasTypedQueries(cfg) {
		nativeNames["queryPath"] = struct{}{}
	}

	handlerNames := map[string]struct{}{}
	for i := range cfg.Routes {
		route := &cfg.Routes[i]
		if err := o.validateRoute(route); err != nil {
			return fmt.Errorf("routes[%d] (%q): %w", i, route.Name, err)
		}
		if route.Query != nil {
			docs := docRegistry[C]{cfg: cfg}
			fields, err := routeQueryFields(&docs, route)
			if err == nil {
				err = o.validateQueryFields(cfg, fields)
			}
			if err != nil {
				return fmt.Errorf("routes[%d] (%q): %w", i, route.Name, err)
			}
		}
		// Scoped TypeScript factories have independent method namespaces.
		namespace := ""
		if scope := routeClientScope(cfg.ClientScopes, route.Path); scope != nil {
			namespace = scope.Name
		}
		if o.TypeScriptDir != "" {
			key := namespace + "/" + route.Name
			if _, ok := names[key]; ok {
				return fmt.Errorf("routes[%d] (%q): duplicate route name in client scope %q", i, route.Name, namespace)
			}
			names[key] = struct{}{}
			if route.IsSSE {
				name := routeTSSSEHandlersName(route)
				if _, ok := handlerNames[name]; ok {
					return fmt.Errorf("routes[%d] (%q): duplicate SSE handler type %q", i, route.Name, name)
				}
				handlerNames[name] = struct{}{}
			}
		}
		if o.KotlinDir != "" || o.SwiftDir != "" {
			methods := []string{route.Name}
			if route.IsSSE {
				methods = append(methods, route.Name+"Reconnecting")
			}
			for _, name := range methods {
				if _, ok := nativeNames[name]; ok {
					return fmt.Errorf("routes[%d] (%q): duplicate native method %q", i, route.Name, name)
				}
				nativeNames[name] = struct{}{}
			}
		}
	}
	return nil
}

func (o *outputPaths) validateQueryFields[C ~string](cfg *apispec.Config[C], fields []queryField) error {
	for _, f := range fields {
		t := f.field.Type
		if t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if m := lookupSpecial(cfg, t); m != nil {
			if o.TypeScriptDir != "" && !slices.Contains([]string{"boolean", "number", "string"}, m.TSType) ||
				o.KotlinDir != "" && !slices.Contains([]string{"Boolean", "Double", "Float", "Int", "Long", "String"}, m.KTType) ||
				o.SwiftDir != "" && !slices.Contains([]string{"Bool", "Double", "Float", "Int", "String"}, m.SwiftType) {
				return fmt.Errorf("query field %q must map to a scalar in each selected language", f.name)
			}
			continue
		}
		switch t.Kind() {
		case reflect.Bool, reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		default:
			return fmt.Errorf("query field %q has nonscalar type %s", f.name, t)
		}
	}
	return nil
}

func (o *outputPaths) validateRoute(route *apispec.Route) error {
	parameters := map[string]struct{}{}
	for _, name := range extractPathParams(route.Path) {
		parameters[name] = struct{}{}
	}
	for _, name := range route.QueryParams {
		parameters[name] = struct{}{}
	}
	reserved := []string{}
	if route.Query != nil {
		reserved = append(reserved, "query")
		if o.TypeScriptDir != "" {
			reserved = append(reserved, "search")
		}
		if o.SwiftDir != "" && route.IsSSE {
			reserved = append(reserved, "queryURL")
		}
	}
	if route.Req != nil {
		reserved = append(reserved, "req")
	}
	if route.HeadersArg {
		reserved = append(reserved, "headers")
	}
	if route.IsSSE && o.TypeScriptDir != "" {
		reserved = append(reserved, "handlers")
	}
	if route.IsSSE && len(route.SSEEvents) != 0 && (o.KotlinDir != "" || o.SwiftDir != "") {
		reserved = append(reserved, "lastEventID", "onEventID")
		for _, event := range route.SSEEvents {
			if _, ok := parameters[event.Handler]; ok {
				return fmt.Errorf("SSE handler %q conflicts with a route parameter", event.Handler)
			}
			if slices.Contains(reserved, event.Handler) {
				return fmt.Errorf("SSE handler %q conflicts with a generated argument", event.Handler)
			}
		}
	}
	for _, name := range reserved {
		if _, ok := parameters[name]; ok {
			return fmt.Errorf("parameter %q conflicts with a generated argument", name)
		}
	}
	for i, event := range route.SSEEvents {
		if event.Handler == "onMessage" || event.Handler == "onError" {
			return fmt.Errorf("SSEEvents[%d]: handler %q is invalid or duplicated", i, event.Handler)
		}
	}
	return nil
}

// Language selects a generated client language.
type Language string

const (
	// Kotlin generates a Kotlin JVM package.
	Kotlin Language = "Kotlin"
	// Swift generates a Swift package.
	Swift Language = "Swift"
	// TypeScript generates a TypeScript package.
	TypeScript Language = "TypeScript"
)

// Version describes one typed API specification in a generated package.
type Version struct {
	name          string
	kotlinPackage string
	render        func(outputPaths, *preparedOutput) error
}

// NewVersion constructs one version from its name, Go source directory, and specification.
// Version names use v followed by a positive major number, such as v1 or v2.
// Each version's Kotlin package namespace must be distinct within the API.
// The caller must not mutate maps, slices, or referenced values in config after
// construction; Generate and Verify read that immutable specification each time.
func NewVersion[C ~string](version, sourceDir string, config apispec.Config[C]) Version {
	return Version{name: version, kotlinPackage: config.KotlinPackage, render: func(out outputPaths, sink *preparedOutput) error {
		return generateVersion(sourceDir, out, config, sink)
	}}
}

// API describes a complete generated SDK package containing one or more versions.
type API struct {
	// Languages selects client languages. An empty slice generates every language.
	// API references are always generated.
	Languages []Language

	outputDir string
	name      string
	versions  []Version
}

// NewAPI constructs a package with one generator-owned output directory.
// Name is an identifier such as VoiceGatewaySDK and names its Swift package.
// All contents of outputDir are generated, including language package manifests.
// Each API must have its own non-overlapping output directory. The output contains
// API.<version>.md references and selected ts, kotlin, and swift packages.
func NewAPI(outputDir, name string, versions ...Version) API {
	return API{outputDir: filepath.Clean(outputDir), name: name, versions: slices.Clone(versions)}
}

// Generate prepares every version and manifest, then updates the package.
// Unchanged files and supported caches in selected language packages are preserved.
// Preparation errors leave existing output unchanged. Publication errors may leave
// incomplete output; fix the error and rerun Generate to rebuild the package.
// The generator owns the entire output directory; do not put handwritten files
// there. It preserves ts/node_modules, kotlin/.gradle, kotlin/.kotlin,
// kotlin/build, swift/.build, and swift/.swiftpm while their language remains
// selected. Removing a language removes its package and caches. Cache paths must
// be directories; cache symlinks are removed without being followed.
func (api *API) Generate() error {
	out, err := api.prepare()
	if err != nil {
		return err
	}
	if err := out.cleanDirectory(api.outputDir, api.outputDir, out.directories()); err != nil {
		return err
	}
	for _, path := range out.paths() {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return err
		}
		previous, err := readOutput(path, len(out.files[path]))
		if err == nil && string(previous) == out.files[path] {
			continue
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read output %s: %w", path, err)
		}
		if err == nil {
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("replace output %s: %w", path, err)
			}
		}
		if err := os.WriteFile(path, []byte(out.files[path]), 0o600); err != nil {
			return fmt.Errorf("write output %s: %w", path, err)
		}
	}
	return nil
}

// Verify reports missing, changed, and unexpected package entries without changing output.
// The supported language-package caches described by [API.Generate] are ignored.
func (api *API) Verify() error {
	prepared, err := api.prepare()
	if err != nil {
		return fmt.Errorf("generate for verification: %w", err)
	}
	if info, err := os.Lstat(api.outputDir); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("SDK output is stale:\nchanged: %s", api.outputDir)
	}
	out := &verifyOutput{expected: prepared.directories()}
	for _, path := range prepared.paths() {
		if err := out.checkFile(path, prepared.files[path]); err != nil {
			return err
		}
	}
	dir := api.outputDir
	err = fs.WalkDir(os.DirFS(dir), ".", func(name string, entry fs.DirEntry, err error) error {
		path := filepath.Join(dir, name)
		if errors.Is(err, os.ErrNotExist) && path == dir {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.IsDir() && isToolCache(name) {
			return filepath.SkipDir
		}
		if _, ok := out.expected[path]; ok {
			if _, file := prepared.files[path]; !file && entry.Type()&os.ModeSymlink != 0 {
				out.differences = append(out.differences, "changed: "+path)
			}
			return nil
		}
		out.differences = append(out.differences, "obsolete: "+path)
		if entry.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("read output directory %s: %w", dir, err)
	}
	if len(out.differences) != 0 {
		slices.Sort(out.differences)
		return fmt.Errorf("SDK output is stale:\n%s", strings.Join(out.differences, "\n"))
	}
	return nil
}

func (api *API) prepare() (*preparedOutput, error) {
	if api.outputDir == "" || api.outputDir == "." || filepath.Dir(api.outputDir) == api.outputDir {
		return nil, errors.New("output directory must be a non-root package directory")
	}
	if len(api.name) == 0 || len(api.name) > 214 {
		return nil, fmt.Errorf("invalid package name %q", api.name)
	}
	for i, c := range api.name {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (i == 0 || c < '0' || c > '9') {
			return nil, fmt.Errorf("invalid package name %q", api.name)
		}
	}
	if len(api.versions) == 0 {
		return nil, errors.New("at least one API version is required")
	}
	langs := api.Languages
	if len(langs) == 0 {
		langs = []Language{Kotlin, Swift, TypeScript}
	}
	for i, lang := range langs {
		if lang != Kotlin && lang != Swift && lang != TypeScript {
			return nil, fmt.Errorf("unknown language %q", lang)
		}
		if slices.Contains(langs[:i], lang) {
			return nil, fmt.Errorf("duplicate language %q", lang)
		}
	}
	versions := slices.Clone(api.versions)
	slices.SortFunc(versions, func(a, b Version) int { return strings.Compare(a.name, b.name) })
	packages := map[string]struct{}{}
	for i, version := range versions {
		if !validVersion(version.name) || version.render == nil {
			return nil, fmt.Errorf("invalid API version %q", version.name)
		}
		if i > 0 && versions[i-1].name == version.name {
			return nil, fmt.Errorf("duplicate API version %q", version.name)
		}
		if slices.Contains(langs, Kotlin) {
			if _, ok := packages[version.kotlinPackage]; ok {
				return nil, fmt.Errorf("duplicate Kotlin package %q", version.kotlinPackage)
			}
			packages[version.kotlinPackage] = struct{}{}
		}
	}
	out := &preparedOutput{files: map[string]string{}}
	for _, version := range versions {
		paths := outputPaths{MarkdownFile: filepath.Join(api.outputDir, "API."+version.name+".md")}
		if slices.Contains(langs, TypeScript) {
			paths.TypeScriptDir = filepath.Join(api.outputDir, "ts", version.name)
		}
		if slices.Contains(langs, Kotlin) {
			paths.KotlinDir = filepath.Join(api.outputDir, "kotlin", "src", "main", "kotlin", filepath.FromSlash(strings.ReplaceAll(version.kotlinPackage, ".", "/")))
		}
		if slices.Contains(langs, Swift) {
			paths.SwiftDir = filepath.Join(api.outputDir, "swift", "Sources", api.name+strings.ToUpper(version.name))
		}
		if err := version.render(paths, out); err != nil {
			return nil, fmt.Errorf("version %s: %w", version.name, err)
		}
	}
	if err := generateManifests(api, versions, langs, out); err != nil {
		return nil, err
	}
	return out, nil
}

func validVersion(name string) bool {
	if len(name) < 2 || name[0] != 'v' || name[1] < '1' || name[1] > '9' {
		return false
	}
	for _, c := range name[2:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// preparedOutput owns the complete file set before publishing any changes.
type preparedOutput struct{ files map[string]string }

func (out *preparedOutput) write(path, content string) error {
	path = filepath.Clean(path)
	if _, ok := out.files[path]; ok {
		return fmt.Errorf("duplicate output file %s", path)
	}
	out.files[path] = content
	return nil
}
func (out *preparedOutput) paths() []string {
	paths := make([]string, 0, len(out.files))
	for path := range out.files {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return paths
}

func (out *preparedOutput) directories() map[string]struct{} {
	dirs := map[string]struct{}{}
	for path := range out.files {
		for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
			dirs[dir] = struct{}{}
			if filepath.Dir(dir) == dir || dir == "." {
				break
			}
		}
	}
	return dirs
}

// cleanDirectory removes obsolete entries and replaces unsafe output kinds before writing.
func (out *preparedOutput) cleanDirectory(root, dir string, dirs map[string]struct{}) error {
	info, err := os.Lstat(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect output %s: %w", dir, err)
	}
	if err == nil && !info.IsDir() {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("replace output directory %s: %w", dir, err)
		}
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create output directory %s: %w", dir, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read output directory %s: %w", dir, err)
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if _, ok := dirs[path]; ok {
			if err := out.cleanDirectory(root, path, dirs); err != nil {
				return err
			}
			continue
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() && isToolCache(rel) {
			continue
		}
		if _, ok := out.files[path]; ok && entry.Type().IsRegular() {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove obsolete output %s: %w", path, err)
		}
	}
	return nil
}

func isToolCache(path string) bool {
	return slices.Contains([]string{"kotlin/.gradle", "kotlin/.kotlin", "kotlin/build", "swift/.build", "swift/.swiftpm", "ts/node_modules"}, filepath.ToSlash(path))
}

type verifyOutput struct {
	expected    map[string]struct{}
	differences []string
}

func (out *verifyOutput) checkFile(path, content string) error {
	path = filepath.Clean(path)
	out.expected[path] = struct{}{}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		out.differences = append(out.differences, "changed: "+path)
		return nil
	}
	got, err := readOutput(path, len(content))
	switch {
	case errors.Is(err, os.ErrNotExist):
		out.differences = append(out.differences, "missing: "+path)
	case err != nil:
		return fmt.Errorf("read %s: %w", path, err)
	case string(got) != content:
		out.differences = append(out.differences, "changed: "+path)
	}
	return nil
}

// readOutput bounds comparison reads to the expected length plus one mismatch byte.
func readOutput(path string, size int) ([]byte, error) {
	// #nosec G304 -- The caller compares only paths in its prepared generated package.
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(size)+1))
	return data, errors.Join(err, f.Close())
}

func generateVersion[C ~string](sourceDir string, output outputPaths, config apispec.Config[C], sink *preparedOutput) error {
	if err := config.Validate(); err != nil {
		return fmt.Errorf("invalid API specification: %w", err)
	}
	if err := output.validate(&config); err != nil {
		return fmt.Errorf("invalid API specification: %w", err)
	}
	docs, err := loadDocsInDir(sourceDir, &config)
	if err != nil {
		return fmt.Errorf("loading docs: %w", err)
	}

	if err := validateSDKTypes(&config, docs.aliases, output); err != nil {
		return fmt.Errorf("invalid SDK types: %w", err)
	}

	for _, special := range config.SpecialTypes {
		name := special.Type.Name()
		if output.TypeScriptDir != "" && special.TSSource != "" {
			if err := sink.write(filepath.Join(output.TypeScriptDir, "custom."+name+".gen.ts"), "// Code generated by gen-api-sdk. DO NOT EDIT.\n"+strings.TrimRight(special.TSSource, "\r\n")+"\n"); err != nil {
				return err
			}
		}
		if output.KotlinDir != "" && special.KotlinSource != "" {
			if err := sink.write(filepath.Join(output.KotlinDir, "Custom"+name+".kt"), "// Code generated by gen-api-sdk. DO NOT EDIT.\npackage "+config.KotlinPackage+"\n\n"+strings.TrimRight(special.KotlinSource, "\r\n")+"\n"); err != nil {
				return err
			}
		}
		if output.SwiftDir != "" && special.SwiftSource != "" {
			if err := sink.write(filepath.Join(output.SwiftDir, "Custom"+name+".swift"), "// Code generated by gen-api-sdk. DO NOT EDIT.\n"+strings.TrimRight(special.SwiftSource, "\r\n")+"\n"); err != nil {
				return err
			}
		}
	}

	if output.TypeScriptDir != "" {
		if err := docs.generateTSTypes(output.TypeScriptDir, sink); err != nil {
			return err
		}
		if err := docs.generateTS(output.TypeScriptDir, sink); err != nil {
			return err
		}
		if err := docs.generateTSValidate(output.TypeScriptDir, sink); err != nil {
			return err
		}
	}
	if output.KotlinDir != "" {
		if err := docs.generateKotlin(output.KotlinDir, sink); err != nil {
			return err
		}
	}
	if output.SwiftDir != "" {
		if err := docs.generateSwift(output.SwiftDir, sink); err != nil {
			return err
		}
	}
	if output.MarkdownFile != "" {
		if err := docs.generateMarkdownDoc(output.MarkdownFile, sink); err != nil {
			return err
		}
	}
	return nil
}
