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

func writeRouteTSJSONMethod[C ~string](d *docRegistry[C], r *apispec.Route, b *strings.Builder, params []string) error {
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
	if !r.QueryFromReq {
		for _, q := range r.QueryParams {
			args = append(args, q+": string")
		}
	}
	hasReq := r.Req != nil
	if hasReq {
		args = append(args, "req: "+routeReqName(r))
	}
	if r.HeadersArg {
		args = append(args, "headers: Record<string, string> = {}")
	}

	queryParams := r.QueryParams
	if r.QueryFromReq {
		queryParams = nil
	}
	tsPath := buildTSPath(r.Path, params, queryParams)
	if r.QueryFromReq {
		if r.Req == nil || r.Req.Kind() != reflect.Struct {
			return fmt.Errorf("%s: query request must be a struct", r.Name)
		}
		fmt.Fprintf(b, "    %s: (%s): Promise<%s> => {\n", r.Name, strings.Join(args, ", "), respType)
		b.WriteString("      const query = new URLSearchParams();\n")
		fields, err := d.jsonFields(r.Req)
		if err != nil {
			return err
		}
		for _, q := range r.QueryParams {
			field := ""
			for _, f := range fields {
				sf := f.field
				if sf.Tag.Get("query") == q {
					field = sf.Name
					if name, _, _ := strings.Cut(sf.Tag.Get("json"), ","); name != "" && name != "-" {
						field = name
					}
					break
				}
			}
			if field == "" {
				return fmt.Errorf("%s: missing query field %q", r.Name, q)
			}
			fmt.Fprintf(b, "      if (req.%s !== undefined && req.%s !== null) query.set(%q, String(req.%s));\n", field, field, q, field)
		}
		fmt.Fprintf(b, "      return request<%s>(%q, %s + (query.size ? `?${query}` : \"\"));\n", respType, r.Method, tsPath)
		b.WriteString("    },\n")
		return nil
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
	return nil
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

func writeRouteTSSSEMethod(r *apispec.Route, b *strings.Builder, params []string) {
	if r.Doc != "" {
		b.WriteString(formatBlockDoc(r.Doc, "    "))
	}
	args := make([]string, 0, len(params)+len(r.QueryParams)+1)
	for _, p := range slices.Concat(params, r.QueryParams) {
		args = append(args, p+": string")
	}
	tsPath := buildTSPath(r.Path, params, r.QueryParams)
	respName := routeRespName(r)
	validatorName := "validate" + respName
	args = append(args, "handlers: "+routeTSSSEHandlersName(r))
	fmt.Fprintf(b, "    %s: (%s): EventSource => {\n", r.Name, strings.Join(args, ", "))
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
			fmt.Fprintf(b, "      if (handlers.%s) {\n", e.Handler)
			fmt.Fprintf(b, "        es.addEventListener(%q, handlers.%s);\n", e.Name, e.Handler)
			b.WriteString("      }\n")
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

func writeRouteKotlinJSONFunc(r *apispec.Route, b *strings.Builder, params []string) {
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
	hasReq := r.Req != nil
	if hasReq {
		args = append(args, "req: "+routeReqName(r))
	}
	if r.HeadersArg {
		args = append(args, "headers: Map<String, String> = emptyMap()")
	}

	ktPath := buildKotlinPath(r.Path, r.QueryParams)

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

func writeRouteKotlinSSEFunc(r *apispec.Route, b *strings.Builder, params []string) {
	if r.Doc != "" {
		b.WriteString(formatBlockDoc(r.Doc, "    "))
	}
	args := make([]string, 0, len(params)+len(r.QueryParams))
	for _, p := range slices.Concat(params, r.QueryParams) {
		args = append(args, p+": String")
	}
	args = append(args, routeKotlinSSECallbackArgs(r)...)
	ktPath := buildKotlinPath(r.Path, r.QueryParams)
	respName := routeRespName(r)
	if len(r.SSEEvents) != 0 {
		fmt.Fprintf(b, "    fun %s(%s): Flow<%s> = namedSSEFlow(%s, lastEventID, onEventID) { type, data ->\n", r.Name, strings.Join(args, ", "), respName, ktPath)
		b.WriteString("        when (type) {\n")
		fmt.Fprintf(b, "            \"message\" -> json.decodeFromString<%s>(data)\n", respName)
		for _, e := range r.SSEEvents {
			fmt.Fprintf(b, "            %q -> { ", e.Name)
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
	args = append(args, routeKotlinSSECallbackArgs(r)...)
	if len(r.SSEEvents) != 0 {
		for _, e := range r.SSEEvents {
			callArgs = append(callArgs, e.Handler+" = "+e.Handler)
		}
		callArgs = append(callArgs, "lastEventID = resumeID.get()", "onEventID = { resumeID.set(it); onEventID?.invoke(it) }")
		fmt.Fprintf(b, "    fun %s(%s): Flow<%s> = flow {\n        val resumeID = java.util.concurrent.atomic.AtomicReference<String?>(lastEventID)\n        reconnectingNamedFlow { %s(%s) }.collect { emit(it) }\n    }\n", reconnectName, strings.Join(args, ", "), routeRespName(r), r.Name, strings.Join(callArgs, ", "))
		return
	}

	fmt.Fprintf(b, "    fun %s(%s): Flow<%s> = reconnectingFlow { %s(%s) }\n",
		reconnectName, strings.Join(args, ", "), routeRespName(r), r.Name, strings.Join(callArgs, ", "))
}

func writeRouteSwiftJSONFunc(r *apispec.Route, b *strings.Builder, params []string) {
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
	hasReq := r.Req != nil
	if hasReq {
		args = append(args, "req: "+routeReqName(r))
	}
	if r.HeadersArg {
		args = append(args, "headers: [String: String] = [:]")
	}
	swiftPath := buildSwiftPath(r.Path, r.QueryParams)

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

func writeRouteSwiftSSEFunc(r *apispec.Route, b *strings.Builder, params []string) {
	if r.Doc != "" {
		b.WriteString(formatSwiftDoc(r.Doc, "    "))
	}
	args := make([]string, 0, len(params)+len(r.QueryParams))
	for _, p := range slices.Concat(params, r.QueryParams) {
		args = append(args, p+": String")
	}
	args = append(args, routeSwiftSSECallbackArgs(r)...)
	swiftPath := buildSwiftPath(r.Path, r.QueryParams)
	respName := routeRespName(r)
	fmt.Fprintf(b, "    public func %s(%s) -> AsyncThrowingStream<%s, Error> {\n", r.Name, strings.Join(args, ", "), respName)
	if len(r.SSEEvents) != 0 {
		fmt.Fprintf(b, "        namedSSEStream(path: %s, lastEventID: lastEventID, onEventID: onEventID) { type, data in\n            switch type {\n            case \"message\": return try self.decoder.decode(%s.self, from: data)\n", swiftPath, respName)
		for _, e := range r.SSEEvents {
			fmt.Fprintf(b, "            case %q: ", e.Name)
			if e.Resp == nil {
				fmt.Fprintf(b, "%s?()", e.Handler)
			} else {
				fmt.Fprintf(b, "let payload = try self.decoder.decode(%s.self, from: data); %s?(payload)", e.Resp.Name(), e.Handler)
			}
			b.WriteString("; return nil\n")
		}
		b.WriteString("            default: return nil\n            }\n        }\n    }\n")
		return
	}
	fmt.Fprintf(b, "        sseStream(path: %s)\n", swiftPath)
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

func writeRouteSwiftReconnectingFunc(r *apispec.Route, b *strings.Builder, params []string) {
	allParams := slices.Concat(params, r.QueryParams)
	args := make([]string, 0, len(allParams))
	callArgs := make([]string, 0, len(allParams))
	for _, p := range allParams {
		args = append(args, p+": String")
		callArgs = append(callArgs, p+": "+p)
	}
	args = append(args, routeSwiftSSECallbackArgs(r)...)
	reconnectName := r.Name + "Reconnecting"
	respName := routeRespName(r)
	fmt.Fprintf(b, "    public func %s(%s) -> AsyncThrowingStream<%s, Error> {\n",
		reconnectName, strings.Join(args, ", "), respName)
	if len(r.SSEEvents) != 0 {
		for _, e := range r.SSEEvents {
			callArgs = append(callArgs, e.Handler+": "+e.Handler)
		}
		callArgs = append(callArgs, "lastEventID: resumeID.get()", "onEventID: { resumeID.set($0); onEventID?($0) }")
		b.WriteString("        let resumeID = SSECursor(lastEventID)\n")
		fmt.Fprintf(b, "        return namedReconnectingStream { self.%s(%s) }\n    }\n", r.Name, strings.Join(callArgs, ", "))
		return
	}
	fmt.Fprintf(b, "        reconnectingStream { self.%s(%s) }\n", r.Name, strings.Join(callArgs, ", "))
	b.WriteString("    }\n")
}

// buildKotlinPath returns a Kotlin string expression for the path. Uses string
// templates for paths with parameters and appends URL-encoded query values.
func buildKotlinPath(path string, queryParams []string) string {
	var b strings.Builder
	quoted := fmt.Sprintf("%q", path)
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
