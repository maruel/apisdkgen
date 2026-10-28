// Package apisdkgen generates typed client SDKs and API references.
// It supports TypeScript, Kotlin, and Swift from Go DTO types and route definitions.
//
// SDK specifications are defined by each package in an exported SDKAPI()
// function returning an apispec.Config with that API's error-code type. The
// consumer's gen-api-sdk command registers those specs and chooses output paths.
package apisdkgen

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/maruel/apisdkgen/apispec"
)

// OutputConfig names the generated output directories for each target.
type OutputConfig struct {
	TypeScriptDir string
	KotlinDir     string
	SwiftDir      string
	MarkdownDir   string
}

// validate checks emitter requirements after Config.Validate succeeds.
func (o *OutputConfig) validate[C ~string](cfg *apispec.Config[C]) error {
	if o.KotlinDir != "" {
		for _, part := range strings.Split(cfg.KotlinPackage, ".") {
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
		if cfg.MCPProtocolVersion != "" {
			for _, name := range []string{"serverDiscover", "serverInstructions"} {
				if _, ok := names["/"+name]; ok {
					return fmt.Errorf("client scope %q conflicts with a generated MCP method", name)
				}
				names["/"+name] = struct{}{}
			}
		}
	}
	nativeNames := map[string]struct{}{}
	if cfg.MCPProtocolVersion != "" && o.KotlinDir != "" {
		nativeNames["serverDiscover"] = struct{}{}
		nativeNames["serverInstructions"] = struct{}{}
	}
	handlerNames := map[string]struct{}{}
	for i := range cfg.Routes {
		route := &cfg.Routes[i]
		if err := o.validateRoute(route); err != nil {
			return fmt.Errorf("routes[%d] (%q): %w", i, route.Name, err)
		}
		if o.TypeScriptDir != "" && route.QueryFromReq {
			docs := docRegistry[C]{cfg: cfg}
			if _, err := routeQueryFields(&docs, route); err != nil {
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

func (o *OutputConfig) validateRoute(route *apispec.Route) error {
	parameters := map[string]struct{}{}
	for _, name := range extractPathParams(route.Path) {
		parameters[name] = struct{}{}
	}
	for _, name := range route.QueryParams {
		parameters[name] = struct{}{}
	}
	reserved := []string{}
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

// API describes one API surface to generate.
type API struct {
	generate func(OutputConfig, outputSink) error
	output   OutputConfig
}

// NewAPI constructs an SDK generation target from a typed API specification.
func NewAPI[C ~string](sourceDir string, output OutputConfig, config apispec.Config[C]) API {
	return API{output: output, generate: func(out OutputConfig, sink outputSink) error {
		return generateAPI(sourceDir, out, config, sink)
	}}
}

// Generate writes configured SDK outputs for one API surface.
// Specification metadata is validated before any output is written.
func (api *API) Generate() error {
	return api.generate(api.output, diskOutput{})
}

// Verify reports missing, changed, and obsolete generated files without modifying outputs.
// Files bearing the generator's header in configured output directories are owned
// by the generator. The TypeScript validator is also owned when no longer generated.
// Other files are ignored. Differences are returned as an error.
func (api *API) Verify() error {
	out := &verifyOutput{expected: map[string]map[string]struct{}{}}
	for _, dir := range []string{api.output.TypeScriptDir, api.output.KotlinDir, api.output.SwiftDir, api.output.MarkdownDir} {
		if dir != "" {
			out.expected[filepath.Clean(dir)] = map[string]struct{}{}
		}
	}
	if err := api.generate(api.output, out); err != nil {
		return fmt.Errorf("generate for verification: %w", err)
	}
	for original, expected := range out.expected {
		entries, err := os.ReadDir(original)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read output directory %s: %w", original, err)
		}
		for _, entry := range entries {
			if _, ok := expected[entry.Name()]; ok || !entry.Type().IsRegular() {
				continue
			}
			path := filepath.Join(original, entry.Name())
			if api.output.TypeScriptDir != "" && filepath.Clean(api.output.TypeScriptDir) == original && entry.Name() == "validate.gen.ts" {
				out.differences = append(out.differences, "obsolete: "+path)
				continue
			}
			f, err := os.Open(path) //nolint:gosec // The configured output path is supplied by the caller.
			if err != nil {
				return fmt.Errorf("open %s: %w", path, err)
			}
			var header [64]byte
			n, readErr := f.Read(header[:])
			closeErr := f.Close()
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				return fmt.Errorf("read %s: %w", path, readErr)
			}
			if closeErr != nil {
				return fmt.Errorf("close %s: %w", path, closeErr)
			}
			if bytes.HasPrefix(header[:n], []byte("// Code generated by gen-api-sdk. DO NOT EDIT.")) || bytes.HasPrefix(header[:n], []byte("<!-- Code generated by gen-api-sdk; DO NOT EDIT. -->")) {
				out.differences = append(out.differences, "obsolete: "+path)
			}
		}
	}
	if len(out.differences) != 0 {
		slices.Sort(out.differences)
		return fmt.Errorf("SDK output is stale:\n%s", strings.Join(out.differences, "\n"))
	}
	return nil
}

type outputSink interface {
	mkdir(string) error
	write(string, string) error
	remove(string) error
}

type diskOutput struct{}

func (diskOutput) mkdir(dir string) error { return os.MkdirAll(dir, 0o750) }
func (diskOutput) write(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}
func (diskOutput) remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

type verifyOutput struct {
	expected    map[string]map[string]struct{}
	differences []string
}

func (*verifyOutput) mkdir(string) error  { return nil }
func (*verifyOutput) remove(string) error { return nil }
func (out *verifyOutput) write(path, content string) error {
	path = filepath.Clean(path)
	out.expected[filepath.Dir(path)][filepath.Base(path)] = struct{}{}
	got, err := os.ReadFile(path)
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

func generateAPI[C ~string](sourceDir string, output OutputConfig, config apispec.Config[C], sink outputSink) error {
	if err := config.Validate(); err != nil {
		return fmt.Errorf("invalid API specification: %w", err)
	}
	if err := output.validate(&config); err != nil {
		return fmt.Errorf("invalid API specification: %w", err)
	}
	docs, err := loadDocsInDir[C](sourceDir)
	if err != nil {
		return fmt.Errorf("loading docs: %w", err)
	}
	docs.cfg = &config
	if err := docs.addConfiguredErrorCodeAlias(); err != nil {
		return fmt.Errorf("configuring error-code type: %w", err)
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
	if output.MarkdownDir != "" {
		if err := docs.generateMarkdownDoc(output.MarkdownDir, sink); err != nil {
			return err
		}
	}
	return nil
}
