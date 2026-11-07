// Route definition and generated client method emitters.

package apisdkgen

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/maruel/apisdkgen/apispec"
)

func routeReqName(r *apispec.Route) string {
	if r.Req == nil {
		return ""
	}
	return r.Req.Name()
}

func routeRespName(r *apispec.Route) string {
	return r.Resp.Name()
}

func routeCategoryName(r *apispec.Route) string {
	if r.Category != "" {
		return r.Category
	}
	p := strings.TrimPrefix(r.Path, "/")
	if i := strings.IndexByte(p, '/'); i >= 0 {
		p = p[:i]
	}
	if p == "" {
		return "Other"
	}
	return strings.ToUpper(p[:1]) + p[1:]
}

func writeRouteTSJSONMethod(r *apispec.Route, b *strings.Builder, params []string, fields []queryField) {
	if r.Doc != "" {
		b.WriteString(formatBlockDoc(r.Doc, "    "))
	}
	respType := routeRespName(r)
	if r.IsArray {
		respType += "[]"
	}

	args := make([]string, 0, len(params)+len(r.QueryParams)+1)
	for _, p := range params {
		args = append(args, p+": string")
	}
	for _, q := range r.QueryParams {
		args = append(args, q+": string")
	}
	if r.Query != nil {
		args = append(args, "query: "+r.Query.Name())
	}
	hasReq := r.Req != nil
	if hasReq {
		args = append(args, "req: "+routeReqName(r))
	}
	if r.HeadersArg {
		args = append(args, "headers: Record<string, string> = {}")
	}

	tsPath := buildTSPath(r.Path, params, r.QueryParams)
	if r.Query != nil {
		fmt.Fprintf(b, "    %s: (%s): Promise<%s> => {\n", r.Name, strings.Join(args, ", "), respType)
		writeTSQuery(b, fields)
		bodyArg := ""
		if hasReq || r.HeadersArg {
			bodyArg = ", undefined"
			if hasReq {
				bodyArg = ", req"
			}
		}
		if r.HeadersArg {
			bodyArg += ", headers"
		}
		fmt.Fprintf(b, "      return request<%s>(%q, %s + (search.size ? `?${search}` : \"\")%s);\n", respType, r.Method, tsPath, bodyArg)
		b.WriteString("    },\n")
		return
	}

	headerArg := ""
	if r.HeadersArg {
		headerArg = ", headers"
	}
	switch {
	case hasReq:
		fmt.Fprintf(b, "    %s: (%s): Promise<%s> => request<%s>(%q, %s, req%s),\n", r.Name, strings.Join(args, ", "), respType, respType, r.Method, tsPath, headerArg)
	case r.HeadersArg:
		fmt.Fprintf(b, "    %s: (%s): Promise<%s> => request<%s>(%q, %s, undefined, headers),\n", r.Name, strings.Join(args, ", "), respType, respType, r.Method, tsPath)
	default:
		fmt.Fprintf(b, "    %s: (%s): Promise<%s> => request<%s>(%q, %s),\n", r.Name, strings.Join(args, ", "), respType, respType, r.Method, tsPath)
	}
}

// writeTSQuery emits the common scalar query encoder for JSON and SSE methods.
func writeTSQuery(b *strings.Builder, fields []queryField) {
	b.WriteString("      const search = new URLSearchParams();\n")
	for _, f := range fields {
		value := "query[" + fmt.Sprintf("%q", f.jsonName) + "]"
		fmt.Fprintf(b, "      if (%s !== undefined && %s !== null) search.set(%q, String(%s));\n", value, value, f.name, value)
	}
}

func routeTSSSEHandlersName(r *apispec.Route) string {
	return strings.ToUpper(r.Name[:1]) + r.Name[1:] + "Handlers"
}

func writeRouteTSSSEHandlers(r *apispec.Route, b *strings.Builder) {
	if !r.IsSSE {
		return
	}
	fmt.Fprintf(b, "export interface %s {\n", routeTSSSEHandlersName(r))
	fmt.Fprintf(b, "  onMessage: (event: %s) => void;\n", routeRespName(r))
	b.WriteString("  // Decode and callback failures are reported here; the EventSource stays open.\n")
	b.WriteString("  onError: (err: unknown) => void;\n")
	for i := range r.SSEEvents {
		e := &r.SSEEvents[i]
		if e.Resp == nil {
			fmt.Fprintf(b, "  %s?: () => void;\n", e.Handler)
		} else {
			fmt.Fprintf(b, "  %s?: (event: %s) => void;\n", e.Handler, e.Resp.Name())
		}
	}
	b.WriteString("}\n\n")
}

func writeRouteTSSSEMethod(r *apispec.Route, b *strings.Builder, params []string, fields []queryField) {
	if r.Doc != "" {
		b.WriteString(formatBlockDoc(r.Doc, "    "))
	}
	args := make([]string, 0, len(params)+len(r.QueryParams)+1)
	for _, p := range slices.Concat(params, r.QueryParams) {
		args = append(args, p+": string")
	}
	if r.Query != nil {
		args = append(args, "query: "+r.Query.Name())
	}
	tsPath := buildTSPath(r.Path, params, r.QueryParams)
	respName := routeRespName(r)
	validatorName := "validate" + respName
	args = append(args, "handlers: "+routeTSSSEHandlersName(r))
	fmt.Fprintf(b, "    %s: (%s): EventSource => {\n", r.Name, strings.Join(args, ", "))
	if r.Query != nil {
		writeTSQuery(b, fields)
		tsPath += " + (search.size ? `?${search}` : \"\")"
	}
	fmt.Fprintf(b, "      const es = new EventSource(%s);\n", tsPath)
	b.WriteString("      es.addEventListener(\"message\", (e) => {\n")
	b.WriteString("        try {\n")
	fmt.Fprintf(b, "          handlers.onMessage(%s(JSON.parse(e.data)));\n", validatorName)
	b.WriteString("        } catch (err) {\n")
	b.WriteString("          handlers.onError(err);\n")
	b.WriteString("        }\n")
	b.WriteString("      });\n")
	for i := range r.SSEEvents {
		e := &r.SSEEvents[i]
		if e.Resp == nil {
			// Payloadless SSE messages still carry string data; native error Events belong to EventSource.
			fmt.Fprintf(b, "      es.addEventListener(%q, (e) => {\n", e.Name)
			b.WriteString("        if (!(e instanceof MessageEvent) || typeof e.data !== \"string\") return;\n")
			b.WriteString("        try {\n")
			fmt.Fprintf(b, "          handlers.%s?.();\n", e.Handler)
			b.WriteString("        } catch (err) {\n")
			b.WriteString("          handlers.onError(err);\n")
			b.WriteString("        }\n")
			b.WriteString("      });\n")
			continue
		}
		fmt.Fprintf(b, "      es.addEventListener(%q, (e) => {\n", e.Name)
		b.WriteString("        if (!(e instanceof MessageEvent) || typeof e.data !== \"string\") return;\n")
		b.WriteString("        try {\n")
		fmt.Fprintf(b, "          handlers.%s?.(validate%s(JSON.parse(e.data)));\n", e.Handler, e.Resp.Name())
		b.WriteString("        } catch (err) {\n")
		b.WriteString("          handlers.onError(err);\n")
		b.WriteString("        }\n")
		b.WriteString("      });\n")
	}
	b.WriteString("      return es;\n")
	b.WriteString("    },\n")
}

func writeRouteKotlinJSONFunc(r *apispec.Route, b *strings.Builder, params []string, fields []queryField) {
	if r.Doc != "" {
		b.WriteString(formatBlockDoc(r.Doc, "    "))
	}
	respType := routeRespName(r)
	if r.IsArray {
		respType = "List<" + respType + ">"
	}

	// Build function parameters.
	args := make([]string, 0, len(params)+len(r.QueryParams)+1)
	for _, p := range params {
		args = append(args, p+": String")
	}
	for _, q := range r.QueryParams {
		args = append(args, q+": String")
	}
	if r.Query != nil {
		args = append(args, "query: "+r.Query.Name())
	}
	hasReq := r.Req != nil
	if hasReq {
		args = append(args, "req: "+routeReqName(r))
	}
	if r.HeadersArg {
		args = append(args, "headers: Map<String, String> = emptyMap()")
	}

	ktPath := buildKotlinPath(r.Path, r.QueryParams)
	if r.Query != nil {
		ktPath = kotlinQueryPath(ktPath, fields)
	}

	sig := strings.Join(args, ", ")
	headersArg := ""
	if r.HeadersArg {
		headersArg = ", headers = headers"
	}
	switch {
	case hasReq:
		fmt.Fprintf(b, "    suspend fun %s(%s): %s = request(%q, %s, json.encodeToString(req)%s)\n", r.Name, sig, respType, r.Method, ktPath, headersArg)
	case r.HeadersArg:
		fmt.Fprintf(b, "    suspend fun %s(%s): %s = request(%q, %s, headers = headers)\n", r.Name, sig, respType, r.Method, ktPath)
	default:
		fmt.Fprintf(b, "    suspend fun %s(%s): %s = request(%q, %s)\n", r.Name, sig, respType, r.Method, ktPath)
	}
}

func writeRouteKotlinSSEFunc(r *apispec.Route, b *strings.Builder, params []string, fields []queryField) {
	if r.Doc != "" {
		b.WriteString(formatBlockDoc(r.Doc, "    "))
	}
	args := make([]string, 0, len(params)+len(r.QueryParams))
	for _, p := range slices.Concat(params, r.QueryParams) {
		args = append(args, p+": String")
	}
	if r.Query != nil {
		args = append(args, "query: "+r.Query.Name())
	}
	args = append(args, routeKotlinSSECallbackArgs(r)...)
	ktPath := buildKotlinPath(r.Path, r.QueryParams)
	if r.Query != nil {
		ktPath = kotlinQueryPath(ktPath, fields)
	}
	respName := routeRespName(r)
	if len(r.SSEEvents) != 0 {
		fmt.Fprintf(b, "    fun %s(%s): Flow<%s> = namedSSEFlow(%s, lastEventID, onEventID) { type, data ->\n", r.Name, strings.Join(args, ", "), respName, ktPath)
		b.WriteString("        when (type) {\n")
		fmt.Fprintf(b, "            \"message\" -> json.decodeFromString<%s>(data)\n", respName)
		for _, e := range r.SSEEvents {
			fmt.Fprintf(b, "            %s -> { ", kotlinStringLiteral(e.Name))
			if e.Resp == nil {
				fmt.Fprintf(b, "%s?.invoke()", e.Handler)
			} else {
				fmt.Fprintf(b, "val payload = json.decodeFromString<%s>(data); %s?.invoke(payload)", e.Resp.Name(), e.Handler)
			}
			b.WriteString("; null }\n")
		}
		b.WriteString("            else -> null\n        }\n    }\n")
		return
	}
	fmt.Fprintf(b, "    fun %s(%s): Flow<%s> = sseFlow<%s>(%s)\n", r.Name, strings.Join(args, ", "), respName, respName, ktPath)
}

func routeKotlinSSECallbackArgs(r *apispec.Route) []string {
	if len(r.SSEEvents) == 0 {
		return nil
	}
	var args []string
	for _, e := range r.SSEEvents {
		typ := ""
		if e.Resp != nil {
			typ = e.Resp.Name()
		}
		args = append(args, e.Handler+": (("+typ+") -> Unit)? = null")
	}
	return append(args, "lastEventID: String? = null", "onEventID: ((String) -> Unit)? = null")
}

func writeRouteKotlinReconnectingFunc(r *apispec.Route, b *strings.Builder, params []string) {
	if r.Doc != "" {
		b.WriteString(formatBlockDoc(r.Doc, "    "))
	}
	// Build the function name: e.g. "taskEvents" -> "taskEventsReconnecting"
	reconnectName := r.Name + "Reconnecting"

	allParams := slices.Concat(params, r.QueryParams)
	args := make([]string, 0, len(allParams))
	callArgs := make([]string, 0, len(allParams))
	for _, p := range allParams {
		args = append(args, p+": String")
		callArgs = append(callArgs, p)
	}
	if r.Query != nil {
		args = append(args, "query: "+r.Query.Name())
		callArgs = append(callArgs, "query")
	}
	args = append(args, routeKotlinSSECallbackArgs(r)...)
	if len(r.SSEEvents) != 0 {
		for _, e := range r.SSEEvents {
			callArgs = append(callArgs, e.Handler+" = "+e.Handler)
		}
		callArgs = append(callArgs, "lastEventID = resumeID.get()", "onEventID = { resumeID.set(it); onEventID?.invoke(it) }")
		fmt.Fprintf(b, "    fun %s(%s): Flow<%s> = flow {\n        // This cold flow starts a fresh cursor for each collection.\n        val resumeID = java.util.concurrent.atomic.AtomicReference<String?>(lastEventID)\n        reconnectingFlow { %s(%s) }.collect { emit(it) }\n    }\n", reconnectName, strings.Join(args, ", "), routeRespName(r), r.Name, strings.Join(callArgs, ", "))
		return
	}

	fmt.Fprintf(b, "    fun %s(%s): Flow<%s> = reconnectingFlow { %s(%s) }\n",
		reconnectName, strings.Join(args, ", "), routeRespName(r), r.Name, strings.Join(callArgs, ", "))
}

func writeRouteSwiftJSONFunc(r *apispec.Route, b *strings.Builder, params []string, fields []queryField) {
	if r.Doc != "" {
		b.WriteString(formatSwiftDoc(r.Doc, "    "))
	}
	respType := routeRespName(r)
	if r.IsArray {
		respType = "[" + respType + "]"
	}

	args := make([]string, 0, len(params)+len(r.QueryParams)+1)
	for _, p := range params {
		args = append(args, p+": String")
	}
	for _, q := range r.QueryParams {
		args = append(args, q+": String")
	}
	if r.Query != nil {
		args = append(args, "query: "+r.Query.Name())
	}
	hasReq := r.Req != nil
	if hasReq {
		args = append(args, "req: "+routeReqName(r))
	}
	if r.HeadersArg {
		args = append(args, "headers: [String: String] = [:]")
	}
	swiftPath := buildSwiftPath(r.Path, r.QueryParams)
	if r.Query != nil {
		swiftPath = swiftQueryPath(swiftPath, fields)
	}

	fmt.Fprintf(b, "    public func %s(%s) async throws -> %s {\n", r.Name, strings.Join(args, ", "), respType)
	headersArg := ""
	if r.HeadersArg {
		headersArg = ", headers: headers"
	}
	if hasReq {
		fmt.Fprintf(b, "        try await request(%q, path: %s, body: try encoder.encode(req)%s)\n", r.Method, swiftPath, headersArg)
	} else {
		fmt.Fprintf(b, "        try await request(%q, path: %s%s)\n", r.Method, swiftPath, headersArg)
	}
	b.WriteString("    }\n")
}

func writeRouteSwiftSSEFunc(r *apispec.Route, b *strings.Builder, params []string, fields []queryField) {
	if r.Doc != "" {
		b.WriteString(formatSwiftDoc(r.Doc, "    "))
	}
	args := make([]string, 0, len(params)+len(r.QueryParams))
	for _, p := range slices.Concat(params, r.QueryParams) {
		args = append(args, p+": String")
	}
	if r.Query != nil {
		args = append(args, "query: "+r.Query.Name())
	}
	args = append(args, routeSwiftSSECallbackArgs(r)...)
	swiftPath := buildSwiftPath(r.Path, r.QueryParams)
	respName := routeRespName(r)
	fmt.Fprintf(b, "    public func %s(%s) -> AsyncThrowingStream<%s, Error> {\n", r.Name, strings.Join(args, ", "), respName)
	indent, result := "        ", ""
	if r.Query != nil {
		b.WriteString("        do {\n")
		indent, result = "            ", "return "
		swiftPath = swiftQueryPath(swiftPath, fields)
	}
	if len(r.SSEEvents) != 0 {
		fmt.Fprintf(b, "%s%snamedSSEStream(path: %s, lastEventID: lastEventID, onEventID: onEventID) { type, data in\n%s    switch type {\n%s    case \"message\": return try self.decoder.decode(%s.self, from: data)\n", indent, result, swiftPath, indent, indent, respName)
		for _, e := range r.SSEEvents {
			fmt.Fprintf(b, "%s    case %q: ", indent, e.Name)
			if e.Resp == nil {
				fmt.Fprintf(b, "%s?()", e.Handler)
			} else {
				fmt.Fprintf(b, "let payload = try self.decoder.decode(%s.self, from: data); %s?(payload)", e.Resp.Name(), e.Handler)
			}
			b.WriteString("; return nil\n")
		}
		fmt.Fprintf(b, "%s    default: return nil\n%s    }\n%s}\n", indent, indent, indent)
	} else {
		fmt.Fprintf(b, "%s%ssseStream(path: %s, decodeFailure: .discard)\n", indent, result, swiftPath)
	}
	if r.Query != nil {
		b.WriteString("        } catch { return AsyncThrowingStream { $0.finish(throwing: error) } }\n")
	}
	b.WriteString("    }\n")
}
func routeSwiftSSECallbackArgs(r *apispec.Route) []string {
	if len(r.SSEEvents) == 0 {
		return nil
	}
	var args []string
	for _, e := range r.SSEEvents {
		typ := ""
		if e.Resp != nil {
			typ = e.Resp.Name()
		}
		args = append(args, e.Handler+": (("+typ+") -> Void)? = nil")
	}
	return append(args, "lastEventID: String? = nil", "onEventID: ((String) -> Void)? = nil")
}

func writeRouteSwiftReconnectingFunc(r *apispec.Route, b *strings.Builder, params []string, fields []queryField) {
	allParams := slices.Concat(params, r.QueryParams)
	args := make([]string, 0, len(allParams))
	callArgs := make([]string, 0, len(allParams))
	for _, p := range allParams {
		args = append(args, p+": String")
		callArgs = append(callArgs, p+": "+p)
	}
	if r.Query != nil {
		args = append(args, "query: "+r.Query.Name())
		callArgs = append(callArgs, "query: query")
	}
	args = append(args, routeSwiftSSECallbackArgs(r)...)
	reconnectName := r.Name + "Reconnecting"
	respName := routeRespName(r)
	fmt.Fprintf(b, "    public func %s(%s) -> AsyncThrowingStream<%s, Error> {\n",
		reconnectName, strings.Join(args, ", "), respName)
	wrapQuery := r.Query != nil && len(r.SSEEvents) == 0
	indent := "        "
	if wrapQuery {
		b.WriteString("        do {\n")
		indent = "            "
	}
	if len(r.SSEEvents) != 0 {
		for _, e := range r.SSEEvents {
			callArgs = append(callArgs, e.Handler+": "+e.Handler)
		}
		callArgs = append(callArgs, "lastEventID: resumeID.get()", "onEventID: { resumeID.set($0); onEventID?($0) }")
		b.WriteString("        // Each returned stream owns its cursor; create another stream for independent consumption.\n        let resumeID = SSECursor(lastEventID)\n")
		fmt.Fprintf(b, "        return reconnectingStream { self.%s(%s) }\n", r.Name, strings.Join(callArgs, ", "))
	} else {
		fmt.Fprintf(b, "%s// Reconnecting plain streams terminate on malformed payloads.\n", indent)
		path := buildSwiftPath(r.Path, r.QueryParams)
		result := ""
		if r.Query != nil {
			fmt.Fprintf(b, "%slet queryURL = %s\n", indent, swiftQueryPath(path, fields))
			path, result = "queryURL", "return "
		}
		fmt.Fprintf(b, "%s%sreconnectingStream { self.sseStream(path: %s, decodeFailure: .terminate) }\n", indent, result, path)
	}
	if wrapQuery {
		b.WriteString("        } catch { return AsyncThrowingStream { $0.finish(throwing: error) } }\n")
	}
	b.WriteString("    }\n")
}

// buildKotlinPath returns a Kotlin string expression for the path. Uses string
// templates for paths with parameters and appends URL-encoded query values.
func buildKotlinPath(path string, queryParams []string) string {
	var b strings.Builder
	quoted := kotlinStringLiteral(path)
	b.WriteString(pathParamRe.ReplaceAllStringFunc(quoted[1:len(quoted)-1], func(match string) string {
		name := match[1 : len(match)-1]
		return "$" + name
	}))
	for i, q := range queryParams {
		if i == 0 {
			b.WriteByte('?')
		} else {
			b.WriteByte('&')
		}
		b.WriteString(q)
		b.WriteString("=${java.net.URLEncoder.encode(")
		b.WriteString(q)
		b.WriteString(", \"UTF-8\")}")
	}
	return "\"" + b.String() + "\""
}

// buildTSPath returns either a quoted string or a template literal for paths
// with path params and/or query params.
func buildTSPath(path string, params, queryParams []string) string {
	if len(params) == 0 && len(queryParams) == 0 && !pathParamRe.MatchString(path) {
		return fmt.Sprintf("%q", path)
	}
	var b strings.Builder
	b.WriteString(pathParamRe.ReplaceAllStringFunc(path, func(match string) string {
		name := match[1 : len(match)-1]
		return "${encodeURIComponent(" + name + ")}"
	}))
	for i, q := range queryParams {
		if i == 0 {
			b.WriteByte('?')
		} else {
			b.WriteByte('&')
		}
		b.WriteString(q)
		b.WriteString("=${encodeURIComponent(")
		b.WriteString(q)
		b.WriteByte(')')
		b.WriteByte('}')
	}
	return "`" + b.String() + "`"
}

// buildSwiftPath returns a Swift string literal for the path.
// Path params are replaced with Swift string interpolation \(name).
// Query params are appended as URL-encoded interpolated strings.
func buildSwiftPath(path string, queryParams []string) string {
	var b strings.Builder
	b.WriteString(pathParamRe.ReplaceAllStringFunc(path, func(match string) string {
		name := match[1 : len(match)-1]
		return "\\(" + name + ")"
	}))
	for i, q := range queryParams {
		if i == 0 {
			b.WriteByte('?')
		} else {
			b.WriteByte('&')
		}
		b.WriteString(q + "=\\(" + q + ".addingPercentEncoding(withAllowedCharacters: CharacterSet(charactersIn: \"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~\")) ?? \"\")")
	}
	return "\"" + b.String() + "\""
}

// queryField connects a query wire name with its generated DTO property.
type queryField struct {
	name     string
	jsonName string
	field    reflect.StructField
}

// routeQueryFields resolves query tags on the DTO's visible JSON fields.
func routeQueryFields[C ~string](d *docRegistry[C], r *apispec.Route) ([]queryField, error) {
	if r.Query == nil {
		return nil, nil
	}
	fields, err := jsonFields(d.cfg, r.Query)
	if err != nil {
		return nil, err
	}
	var walk func(reflect.Type, []int, []reflect.Type) error
	walk = func(owner reflect.Type, parent []int, active []reflect.Type) error {
		for sf := range owner.Fields() {
			index := append(slices.Clone(parent), sf.Index...)
			if name := sf.Tag.Get("query"); name != "" && name != "-" {
				visible := slices.ContainsFunc(fields, func(f jsonField) bool { return slices.Equal(f.index, index) })
				if !visible {
					return fmt.Errorf("%s.%s: query field %q is not visible in JSON", owner, sf.Name, name)
				}
			}
			base := sf.Type
			if base.Kind() == reflect.Pointer {
				base = base.Elem()
			}
			jsonName, _ := parseJSONTag(sf.Tag.Get("json"))
			if sf.Anonymous && base.Kind() == reflect.Struct && (jsonName == "" || sf.Tag.Get("json") == "-") && !slices.Contains(active, base) {
				if err := walk(base, index, append(slices.Clone(active), base)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(r.Query, nil, []reflect.Type{r.Query}); err != nil {
		return nil, err
	}
	var result []queryField
	var names []string
	for _, f := range fields {
		name := f.field.Tag.Get("query")
		if name == "" || name == "-" {
			continue
		}
		if !isIdentifier(name) || slices.Contains(names, name) {
			return nil, fmt.Errorf("invalid or duplicate query field %q", name)
		}
		names = append(names, name)
		jsonName, _ := parseJSONTag(f.field.Tag.Get("json"))
		if jsonName == "" {
			jsonName = f.field.Name
		}
		result = append(result, queryField{name, jsonName, f.field})
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("query type %s has no query-tagged JSON fields", r.Query)
	}
	return result, nil
}

func kotlinQueryPath(path string, fields []queryField) string {
	pairs := make([]string, 0, len(fields))
	for _, f := range fields {
		pairs = append(pairs, fmt.Sprintf("%s to %s", kotlinStringLiteral(f.name), kotlinStringLiteral(f.jsonName)))
	}
	return "this.queryPath(" + path + ", query, mapOf(" + strings.Join(pairs, ", ") + "))"
}

func swiftQueryPath(path string, fields []queryField) string {
	pairs := make([]string, 0, len(fields))
	for _, f := range fields {
		pairs = append(pairs, fmt.Sprintf("(%q, %q)", f.name, f.jsonName))
	}
	return "try self.queryPath(" + path + ", query, [" + strings.Join(pairs, ", ") + "])"
}
