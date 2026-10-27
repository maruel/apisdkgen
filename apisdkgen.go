// Package apisdkgen generates typed client SDKs and API references.
// It supports TypeScript, Kotlin, and Swift from Go DTO types and route definitions.
//
// SDK specifications are defined by each package in an exported SDKAPI()
// function returning an apispec.Config with that API's error-code type. The
// consumer's gen-api-sdk command registers those specs and chooses output paths.
package apisdkgen

import (
	"errors"
	"fmt"
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
	generate func() error
}

// NewAPI constructs an SDK generation target from a typed API specification.
func NewAPI[C ~string](sourceDir string, output OutputConfig, config apispec.Config[C]) API {
	return API{generate: func() error {
		return generateAPI(sourceDir, output, config)
	}}
}

// Generate writes configured SDK outputs for one API surface.
// Specification metadata is validated before any output is written.
func Generate(api *API) error {
	if api == nil {
		return errors.New("api is nil")
	}
	if api.generate == nil {
		return errors.New("api has no generator")
	}
	return api.generate()
}

func generateAPI[C ~string](sourceDir string, output OutputConfig, config apispec.Config[C]) error {
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
		if err := docs.generateTSTypes(output.TypeScriptDir); err != nil {
			return err
		}
		if err := docs.generateTS(output.TypeScriptDir); err != nil {
			return err
		}
		if err := docs.generateTSValidate(output.TypeScriptDir); err != nil {
			return err
		}
	}
	if output.KotlinDir != "" {
		if err := docs.generateKotlin(output.KotlinDir); err != nil {
			return err
		}
	}
	if output.SwiftDir != "" {
		if err := docs.generateSwift(output.SwiftDir); err != nil {
			return err
		}
	}
	if output.MarkdownDir != "" {
		if err := docs.generateMarkdownDoc(output.MarkdownDir); err != nil {
			return err
		}
	}
	return nil
}
