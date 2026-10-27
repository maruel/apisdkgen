// Package apispec exposes shared API SDK generation specification types.
package apispec

import (
	"errors"
	"fmt"
	"go/token"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

// ClientErrorModel describes how generated clients extract API error details.
type ClientErrorModel struct {
	TypeName string

	TSCodeExpr    string
	TSMessageExpr string
	TSDetailsExpr string

	KTCodeExpr    string
	KTMessageExpr string
	KTDetailsExpr string

	SwiftCodeExpr    string
	SwiftMessageExpr string
	SwiftDetailsExpr string
}

// Config holds configuration for SDK code generation.
type Config[C ~string] struct {
	Routes             []Route
	ClientScopes       []ClientScope
	SDKPackagePaths    map[string]struct{}
	ExtraSeeds         []reflect.Type
	DocumentExtraSeeds bool

	ErrorModel         ClientErrorModel
	KotlinPackage      string
	APIDocTitle        string
	APIDocIntro        string
	ErrorDoc           string
	MCPProtocolVersion string
	SectionComments    map[string]string
	SpecialTypes       []SpecialType
	// Discriminated names types using kind-based dispatch in TS validators.
	// If kind has known string enum values, matching optional JSON fields are
	// required variant payloads; other fields are common metadata. Without enum
	// values, all optional fields are treated as variant payloads.
	Discriminated []string
	ErrorCodes    []ErrorCodeSpec[C]
}

// Validate checks specification metadata independently of the selected outputs.
//
// The generator additionally checks output-specific requirements, such as
// KotlinPackage, generated-name conflicts, and query-field resolution.
// Custom target-language expressions and DTO field mappings are checked during
// generation or compilation, rather than by this method.
func (c *Config[C]) Validate() error {
	for i, t := range c.ExtraSeeds {
		if t == nil {
			return fmt.Errorf("ExtraSeeds[%d]: type must not be nil", i)
		}
	}
	for i, special := range c.SpecialTypes {
		if special.Type == nil {
			return fmt.Errorf("SpecialTypes[%d]: type must not be nil", i)
		}
	}
	names := map[string]struct{}{}
	for i := range c.ClientScopes {
		scope := &c.ClientScopes[i]
		if !isIdentifier(scope.Name) {
			return fmt.Errorf("ClientScopes[%d]: invalid name %q", i, scope.Name)
		}
		if _, ok := names[scope.Name]; ok {
			return fmt.Errorf("ClientScopes[%d]: duplicate name %q", i, scope.Name)
		}
		names[scope.Name] = struct{}{}
		if err := validatePath(scope.PathPrefix); err != nil {
			return fmt.Errorf("ClientScopes[%d]: %w", i, err)
		}
		if !isIdentifier(scope.Parameter) || !slices.Contains(extractPathParams(scope.PathPrefix), scope.Parameter) {
			return fmt.Errorf("ClientScopes[%d]: parameter %q must occur in PathPrefix", i, scope.Parameter)
		}
	}
	for i := range c.Routes {
		route := &c.Routes[i]
		if err := route.Validate(); err != nil {
			return fmt.Errorf("routes[%d] (%q): %w", i, route.Name, err)
		}
	}
	return nil
}

// ErrorCodeSpec describes an API error code for docs and generated constants.
type ErrorCodeSpec[C ~string] struct {
	Code   C
	Status int
}

// ClientScope binds one path parameter in a named TypeScript client factory.
type ClientScope struct {
	Name       string
	PathPrefix string
	Parameter  string
}

// Route describes a single API endpoint for generated clients and docs.
type Route struct {
	Name         string
	Doc          string
	Method       string
	Path         string
	Category     string
	Req          reflect.Type
	Resp         reflect.Type
	IsArray      bool
	IsSSE        bool
	SSEEvents    []SSEEvent
	QueryParams  []string
	QueryFromReq bool
	HeadersArg   bool
}

// Validate checks the route's metadata independently of any selected output.
// Generated-name conflicts and DTO field mappings are checked by the generator.
func (r *Route) Validate() error {
	if !isIdentifier(r.Name) {
		return fmt.Errorf("invalid name %q", r.Name)
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
	case "":
		if !r.IsSSE {
			return errors.New("HTTP method is required")
		}
	default:
		return fmt.Errorf("unsupported HTTP method %q", r.Method)
	}
	if r.IsSSE && r.Method != "" && r.Method != http.MethodGet {
		return errors.New("SSE method must be GET")
	}
	if err := validatePath(r.Path); err != nil {
		return err
	}
	if err := validateNamedType("response", r.Resp); err != nil {
		return err
	}
	if r.Req != nil {
		if err := validateNamedType("request", r.Req); err != nil {
			return err
		}
	}
	if r.QueryFromReq && (r.Req == nil || r.Req.Kind() != reflect.Struct) {
		return errors.New("query request must be a struct")
	}
	parameters := map[string]struct{}{}
	for _, name := range extractPathParams(r.Path) {
		parameters[name] = struct{}{}
	}
	for _, name := range r.QueryParams {
		if !isIdentifier(name) {
			return fmt.Errorf("invalid query parameter %q", name)
		}
		if _, ok := parameters[name]; ok {
			return fmt.Errorf("duplicate parameter %q", name)
		}
		parameters[name] = struct{}{}
	}
	if !r.IsSSE && len(r.SSEEvents) != 0 {
		return errors.New("SSEEvents requires IsSSE")
	}
	events := map[string]struct{}{"message": {}}
	handlers := map[string]struct{}{}
	for i, event := range r.SSEEvents {
		_, duplicateEvent := events[event.Name]
		if event.Name == "" || duplicateEvent {
			return fmt.Errorf("SSEEvents[%d]: event name %q is empty or duplicated", i, event.Name)
		}
		events[event.Name] = struct{}{}
		_, duplicateHandler := handlers[event.Handler]
		if !isIdentifier(event.Handler) || duplicateHandler {
			return fmt.Errorf("SSEEvents[%d]: handler %q is invalid or duplicated", i, event.Handler)
		}
		handlers[event.Handler] = struct{}{}
		if event.Resp != nil {
			if err := validateNamedType("response", event.Resp); err != nil {
				return fmt.Errorf("SSEEvents[%d]: %w", i, err)
			}
		}
	}
	return nil
}

// SSEEvent describes a named event emitted in addition to an SSE route's
// primary message event. A nil Resp denotes a payloadless notification.
type SSEEvent struct {
	Name    string
	Handler string
	Resp    reflect.Type
}

// SpecialType defines custom mappings for Go types that need non-standard serialization.
type SpecialType struct {
	Type       reflect.Type
	TSType     string // TypeScript type
	KTType     string // Kotlin type
	SwiftType  string // Swift type
	DocType    string // API doc type
	TSValidate string // TS validator format string: %[1]s=pathExpr, %[2]q=pathLit
}

func validateNamedType(label string, t reflect.Type) error {
	if t == nil {
		return fmt.Errorf("%s type is required", label)
	}
	if t.Name() == "" {
		return fmt.Errorf("%s type %s must be named", label, t)
	}
	return nil
}

func validatePath(path string) error {
	if path != "" && !strings.HasPrefix(path, "/") {
		return fmt.Errorf("path %q must start with /", path)
	}
	remainder := pathParamRe.ReplaceAllString(path, "")
	if strings.ContainsAny(remainder, "{}\r\n\t ?#") {
		return fmt.Errorf("path %q contains malformed parameters or URL delimiters", path)
	}
	seen := map[string]struct{}{}
	for _, name := range extractPathParams(path) {
		if !isIdentifier(name) {
			return fmt.Errorf("path %q has invalid parameter %q", path, name)
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("path %q repeats parameter %q", path, name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

var pathParamRe = regexp.MustCompile(`\{(\w+)\}`)

func isIdentifier(name string) bool {
	// SDK identifiers may use Go keywords, such as a JSON field named "type".
	return token.IsIdentifier(name) || token.Lookup(name).IsKeyword()
}

func extractPathParams(path string) []string {
	matches := pathParamRe.FindAllStringSubmatch(path, -1)
	out := make([]string, len(matches))
	for i, m := range matches {
		out[i] = m[1]
	}
	return out
}
