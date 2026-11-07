// SDK generation configuration and configured type helpers.

package apisdkgen

import (
	"fmt"
	"maps"
	"reflect"
	"slices"

	"github.com/maruel/apisdkgen/apispec"
)

// lookupSpecial returns the matching SpecialType entry for t, or nil.
func lookupSpecial[C ~string](c *apispec.Config[C], t reflect.Type) *apispec.SpecialType {
	for i := range c.SpecialTypes {
		if c.SpecialTypes[i].Type == t {
			return &c.SpecialTypes[i]
		}
	}
	return nil
}

func isSDKPkg[C ~string](c *apispec.Config[C], pkgPath string) bool {
	_, ok := c.SDKPackagePaths[pkgPath]
	return ok
}

func clientErrorModel[C ~string](c *apispec.Config[C]) apispec.ClientErrorModel {
	m := c.ErrorModel
	if m.TypeName == "" {
		m.TypeName = "ErrorResponse"
	}
	if m.TSCodeExpr == "" {
		m.TSCodeExpr = "err.error.code"
	}
	if m.TSMessageExpr == "" {
		m.TSMessageExpr = "err.error.message"
	}
	if m.TSDetailsExpr == "" {
		m.TSDetailsExpr = "err.details"
	}
	if m.KTCodeExpr == "" {
		m.KTCodeExpr = "err.error.code"
	}
	if m.KTMessageExpr == "" {
		m.KTMessageExpr = "err.error.message"
	}
	if m.KTDetailsExpr == "" {
		m.KTDetailsExpr = "err.details"
	}
	if m.SwiftCodeExpr == "" {
		m.SwiftCodeExpr = "errResp.error.code"
	}
	if m.SwiftMessageExpr == "" {
		m.SwiftMessageExpr = "errResp.error.message"
	}
	if m.SwiftDetailsExpr == "" {
		m.SwiftDetailsExpr = "nil"
	}
	return m
}

// sdkVisitState distinguishes a cycle edge from a completed, shared dependency.
type sdkVisitState uint8

const (
	sdkVisitActive sdkVisitState = iota + 1
	sdkVisitComplete
)

// walkSDKTypes returns SDK named types once in deterministic dependency order.
// Acyclic dependencies precede their users; cycle edges stop at active nodes.
// JSON-selected fields and configured special types define the emitted graph.
func walkSDKTypes[C ~string](c *apispec.Config[C], seeds []reflect.Type) ([]reflect.Type, error) {
	state := map[reflect.Type]sdkVisitState{}
	var order []reflect.Type
	var walk func(reflect.Type) error
	walk = func(t reflect.Type) error {
		if state[t] == sdkVisitActive {
			if (t.Kind() == reflect.Slice || t.Kind() == reflect.Map) && !isSDKPkg(c, t.PkgPath()) {
				return fmt.Errorf("recursive container %s must belong to an SDK package or have a SpecialType mapping", t)
			}
			if t.Kind() == reflect.Pointer {
				return fmt.Errorf("recursive pointer type %s is unsupported", t)
			}
			return nil
		}
		if state[t] == sdkVisitComplete {
			return nil
		}
		state[t] = sdkVisitActive
		if t.Kind() == reflect.Pointer {
			if err := walk(t.Elem()); err != nil {
				return err
			}
		} else if lookupSpecial(c, t) == nil {
			switch t.Kind() {
			case reflect.Struct:
				if !isSDKPkg(c, t.PkgPath()) {
					state[t] = sdkVisitComplete
					return nil
				}
				fields, err := jsonFields(c, t)
				if err != nil {
					return err
				}
				for _, f := range fields {
					if err := walk(f.field.Type); err != nil {
						return err
					}
				}
			case reflect.Slice:
				if err := walk(t.Elem()); err != nil {
					return err
				}
			case reflect.Map:
				if err := walk(t.Key()); err != nil {
					return err
				}
				if err := walk(t.Elem()); err != nil {
					return err
				}
			}
			if t.Name() != "" && isSDKPkg(c, t.PkgPath()) {
				order = append(order, t)
			}
		}
		state[t] = sdkVisitComplete
		return nil
	}
	for _, t := range seeds {
		if err := walk(t); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// collectNamedContainers includes nested and seed aliases, not only direct fields.
func collectNamedContainers[C ~string](c *apispec.Config[C]) ([]reflect.Type, error) {
	types, err := walkSDKTypes(c, sdkSeedTypes(c))
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(types, func(t reflect.Type) bool { return t.Kind() != reflect.Slice && t.Kind() != reflect.Map }), nil
}

// validateSDKTypes owns generated names and the selected targets' cycle policies.
func validateSDKTypes[C ~string](c *apispec.Config[C], aliases []aliasInfo, out OutputConfig) error {
	types, err := walkSDKTypes(c, sdkSeedTypes(c))
	if err != nil {
		return err
	}
	if err := validateSDKNames(c, aliases, types, out); err != nil {
		return err
	}
	return validateSDKCycles(c, types, out)
}

// validateSDKNames checks declaration identity and each selected output namespace.
func validateSDKNames[C ~string](c *apispec.Config[C], aliases []aliasInfo, types []reflect.Type, out OutputConfig) error {
	aliasNames := map[string]struct{}{}
	for _, a := range aliases {
		aliasNames[a.name] = struct{}{}
	}
	names := map[string]reflect.Type{}
	for _, t := range types {
		_, alias := aliasNames[t.Name()]
		if t.Kind() != reflect.Struct && t.Kind() != reflect.Slice && t.Kind() != reflect.Map && !alias {
			continue
		}
		if prior, ok := names[t.Name()]; ok && prior != t {
			return fmt.Errorf("generated type name %q is shared by %s and %s", t.Name(), prior, t)
		}
		if !isIdentifier(t.Name()) {
			return fmt.Errorf("invalid generated type name %q for %s", t.Name(), t)
		}
		names[t.Name()] = t
	}
	for _, a := range aliases {
		if t, ok := names[a.name]; ok && t.Kind() != reflect.String {
			return fmt.Errorf("generated type name %q is shared by %s and a string alias", a.name, t)
		}
		if _, ok := names[a.name]; !ok {
			names[a.name] = nil
		}
	}
	tsImports := map[string]struct{}{}
	for _, r := range c.Routes {
		tsImports[r.Resp.Name()] = struct{}{}
		if r.Req != nil {
			tsImports[r.Req.Name()] = struct{}{}
		}
		for _, e := range r.SSEEvents {
			if e.Resp != nil {
				tsImports[e.Resp.Name()] = struct{}{}
			}
		}
	}
	if len(c.Routes) > 0 || c.MCPProtocolVersion != "" {
		tsImports[clientErrorModel(c).TypeName] = struct{}{}
	}
	if len(c.ErrorCodes) > 0 {
		tsImports[reflect.TypeFor[C]().Name()] = struct{}{}
	}
	if c.MCPProtocolVersion != "" {
		for _, n := range []string{"ClientCapabilities", "Implementation", "RequestMeta", "ServerDiscoverResult"} {
			tsImports[n] = struct{}{}
		}
	}
	tsReserved := map[string]struct{}{"ISOTimestamp": {}}
	for _, n := range []string{"APIClient", "APIError", "AbortSignal", "Error", "EventSource", "FetchFn", "JSON", "MessageEvent", "Promise", "Record", "RequestInit", "Response", "String", "URLSearchParams", "globalThis"} {
		if _, ok := tsImports[n]; ok {
			tsReserved[n] = struct{}{}
		}
	}
	for i := range c.Routes {
		if c.Routes[i].IsSSE {
			n := routeTSSSEHandlersName(&c.Routes[i])
			if _, ok := tsImports[n]; ok {
				tsReserved[n] = struct{}{}
			}
		}
	}
	if out.TypeScriptDir != "" && hasSSERoutes(c) {
		var seeds []reflect.Type
		for _, r := range c.Routes {
			if r.IsSSE {
				seeds = append(seeds, r.Resp)
				for _, e := range r.SSEEvents {
					if e.Resp != nil {
						seeds = append(seeds, e.Resp)
					}
				}
			}
		}
		needed, err := walkSDKTypes(c, seeds)
		if err != nil {
			return err
		}
		for _, t := range needed {
			if t.Kind() != reflect.Struct {
				if _, ok := aliasNames[t.Name()]; !ok {
					continue
				}
			}
			switch t.Name() {
			case "Array", "JSON", "Number", "Object", "ObjectValue", "Record", "TypeError", "ValidatorInput", "ValueValidator":
				tsReserved[t.Name()] = struct{}{}
			}
		}
	}
	ktReserved := map[string]struct{}{}
	for _, n := range []string{"ApiClient", "ApiException", "Boolean", "Double", "Instant", "InstantSerializer", "Int", "JsonElement", "List", "Long", "Map", "String"} {
		ktReserved[n] = struct{}{}
	}
	swReserved := map[string]struct{}{}
	for _, n := range []string{"ApiClient", "ApiError", "Bool", "Double", "ISOTimestamp", "Int", "JSONValue", "String"} {
		swReserved[n] = struct{}{}
	}
	// Private transport types only shadow DTOs referenced inside the client.
	for _, n := range []string{"SSEBackoff", "SSEFrame", "SSEFrameParser", "SSETransportFailure"} {
		if _, ok := tsImports[n]; ok {
			ktReserved[n] = struct{}{}
		}
	}
	for _, n := range []string{"SSEBackoff", "SSECursor", "SSEDecodeFailure", "SSEFrame", "SSEFrameParser", "SSEFrameReader", "SSEReconnectReader", "SSETaskOwner", "SSETransportFailure"} {
		if _, ok := tsImports[n]; ok {
			swReserved[n] = struct{}{}
		}
	}
	for _, a := range aliases {
		ktReserved[a.name+"Serializer"] = struct{}{}
	}
	if len(c.ErrorCodes) == 0 {
		ktReserved["ErrorCodes"] = struct{}{}
		swReserved["ErrorCodes"] = struct{}{}
	}
	tsValues := map[string]struct{}{}
	for _, a := range aliases {
		members := map[string]struct{}{}
		for _, value := range a.constants {
			if !isIdentifier(value.name) && out.TypeScriptDir != "" {
				return fmt.Errorf("invalid TypeScript value name %q", value.name)
			}
			if out.TypeScriptDir != "" {
				if _, ok := tsValues[value.name]; ok {
					return fmt.Errorf("TypeScript value name %q is duplicated", value.name)
				}
				tsValues[value.name] = struct{}{}
			}
			member := a.shortName(value)
			if !isIdentifier(member) && (out.KotlinDir != "" || out.SwiftDir != "") {
				return fmt.Errorf("invalid native enum member %q", a.name+"."+member)
			}
			if out.KotlinDir != "" && member == "Other" {
				return fmt.Errorf("generated Kotlin enum member %q conflicts with a generated declaration", a.name+"."+member)
			}
			if _, ok := members[member]; ok && (out.KotlinDir != "" || out.SwiftDir != "") {
				return fmt.Errorf("native enum member %q is duplicated", a.name+"."+member)
			}
			members[member] = struct{}{}
		}
	}
	for _, n := range slices.Sorted(maps.Keys(names)) {
		if _, ok := tsReserved[n]; ok && out.TypeScriptDir != "" {
			return fmt.Errorf("TypeScript type name %q conflicts with a generated declaration", n)
		}
		if _, ok := ktReserved[n]; ok && out.KotlinDir != "" {
			return fmt.Errorf("generated Kotlin type name %q conflicts with a generated declaration", n)
		}
		if _, ok := swReserved[n]; ok && out.SwiftDir != "" {
			return fmt.Errorf("generated Swift type name %q conflicts with a generated declaration", n)
		}
	}
	return nil
}

// validateSDKCycles rejects only layouts that the selected native emitters cannot represent.
func validateSDKCycles[C ~string](c *apispec.Config[C], types []reflect.Type, out OutputConfig) error {
	for _, target := range []string{"Kotlin", "Swift"} {
		if target == "Kotlin" && out.KotlinDir == "" || target == "Swift" && out.SwiftDir == "" {
			continue
		}
		state := map[reflect.Type]sdkVisitState{}
		var aliasesWalk func(reflect.Type) error
		aliasesWalk = func(t reflect.Type) error {
			if lookupSpecial(c, t) != nil || t.Kind() == reflect.Struct {
				return nil
			}
			if state[t] == sdkVisitActive {
				return fmt.Errorf("%s does not support recursive container alias %s", target, t)
			}
			if state[t] == sdkVisitComplete {
				return nil
			}
			state[t] = sdkVisitActive
			switch t.Kind() {
			case reflect.Pointer, reflect.Slice:
				if err := aliasesWalk(t.Elem()); err != nil {
					return err
				}
			case reflect.Map:
				if err := aliasesWalk(t.Key()); err != nil {
					return err
				}
				if err := aliasesWalk(t.Elem()); err != nil {
					return err
				}
			}
			state[t] = sdkVisitComplete
			return nil
		}
		for _, t := range types {
			if err := aliasesWalk(t); err != nil {
				return err
			}
		}
	}
	if out.SwiftDir != "" {
		state := map[reflect.Type]sdkVisitState{}
		var inline func(reflect.Type) error
		inline = func(t reflect.Type) error {
			if t.Kind() == reflect.Pointer {
				return inline(t.Elem())
			}
			if t.Kind() != reflect.Struct || !isSDKPkg(c, t.PkgPath()) || lookupSpecial(c, t) != nil {
				return nil
			}
			if state[t] == sdkVisitActive {
				return fmt.Errorf("unsupported Swift inline recursive struct %s", t)
			}
			if state[t] == sdkVisitComplete {
				return nil
			}
			state[t] = sdkVisitActive
			fields, err := jsonFields(c, t)
			if err != nil {
				return err
			}
			for _, f := range fields {
				if err := inline(f.field.Type); err != nil {
					return err
				}
			}
			state[t] = sdkVisitComplete
			return nil
		}
		for _, t := range types {
			if err := inline(t); err != nil {
				return err
			}
		}
	}
	return nil
}

// routeSeedTypes returns the set of request and response types from the
// route table, used to seed type discovery for SDK generation.
func routeSeedTypes[C ~string](c *apispec.Config[C]) []reflect.Type {
	var seeds []reflect.Type
	for i := range c.Routes {
		r := &c.Routes[i]
		if r.Req != nil {
			seeds = append(seeds, r.Req)
		}
		seeds = append(seeds, r.Resp)
		for j := range r.SSEEvents {
			if r.SSEEvents[j].Resp != nil {
				seeds = append(seeds, r.SSEEvents[j].Resp)
			}
		}
	}
	return seeds
}

func hasSSERoutes[C ~string](c *apispec.Config[C]) bool {
	for i := range c.Routes {
		if c.Routes[i].IsSSE {
			return true
		}
	}
	return false
}

func sdkSeedTypes[C ~string](c *apispec.Config[C]) []reflect.Type {
	seeds := routeSeedTypes(c)
	seeds = append(seeds, c.ExtraSeeds...)
	return seeds
}

// tsPrimitiveValidator returns the validator expression for a primitive/special type.
// For special types with a tsValidate template, it formats the expression using
// pathExpr and pathLit. Otherwise it falls back to kind-based validation.
func tsPrimitiveValidator[C ~string](c *apispec.Config[C], t reflect.Type, pathExpr, pathLit string) (string, error) {
	if m := lookupSpecial(c, t); m != nil && m.TSValidate != "" {
		return fmt.Sprintf(m.TSValidate, pathExpr, pathLit), nil
	}
	switch t.Kind() {
	case reflect.String:
		return fmt.Sprintf("asString(%s, %q)", pathExpr, pathLit), nil
	case reflect.Int, reflect.Int32, reflect.Int64, reflect.Uint64, reflect.Float64:
		return fmt.Sprintf("asNumber(%s, %q)", pathExpr, pathLit), nil
	case reflect.Bool:
		return fmt.Sprintf("asBoolean(%s, %q)", pathExpr, pathLit), nil
	default:
		return "", fmt.Errorf("tsPrimitiveValidator: unhandled kind %s for %s", t.Kind(), t)
	}
}

// tsElemValidatorFunc returns a function expression (v: unknown) => validated
// for use as the element validator callback in validateArray/validateRecord.
func tsElemValidatorFunc[C ~string](c *apispec.Config[C], t reflect.Type, pathLit string) (string, error) {
	if t.Kind() == reflect.Pointer {
		return tsElemValidatorFunc(c, t.Elem(), pathLit)
	}
	if t.Kind() == reflect.Struct && isSDKPkg(c, t.PkgPath()) {
		return "validate" + t.Name(), nil
	}
	v, err := tsPrimitiveValidator(c, t, "v", pathLit)
	if err != nil {
		return "", err
	}
	return "(v) => " + v, nil
}

// goTypeToDoc maps a Go reflect.Type to a TypeScript-style string for docs.
func goTypeToDoc[C ~string](c *apispec.Config[C], t reflect.Type) (string, error) {
	if t.Kind() == reflect.Pointer {
		return goTypeToDoc(c, t.Elem())
	}
	if m := lookupSpecial(c, t); m != nil {
		return m.DocType, nil
	}
	if (t.Kind() == reflect.Slice || t.Kind() == reflect.Map) && t.Name() != "" && isSDKPkg(c, t.PkgPath()) {
		return t.Name(), nil
	}
	if t.Kind() == reflect.String && t.Name() != "" && isSDKPkg(c, t.PkgPath()) {
		return t.Name(), nil
	}
	switch t.Kind() {
	case reflect.String:
		return "string", nil
	case reflect.Int:
		return "int", nil
	case reflect.Int32:
		return "int32", nil
	case reflect.Int64:
		return "int64", nil
	case reflect.Uint64:
		return "uint64", nil
	case reflect.Float64:
		return "float64", nil
	case reflect.Bool:
		return "boolean", nil
	case reflect.Slice:
		if isSDKPkg(c, t.PkgPath()) {
			return t.Name(), nil // Named slice.
		}
		e, err := goTypeToDoc(c, t.Elem())
		if err != nil {
			return "", err
		}
		return e + "[]", nil
	case reflect.Struct:
		return t.Name(), nil
	case reflect.Map:
		k, err := goTypeToDoc(c, t.Key())
		if err != nil {
			return "", err
		}
		v, err := goTypeToDoc(c, t.Elem())
		if err != nil {
			return "", err
		}
		return "Record<" + k + ", " + v + ">", nil
	default:
		return "", fmt.Errorf("goTypeToDoc: unhandled kind %s for %s", t.Kind(), t)
	}
}
