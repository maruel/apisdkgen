// Generic typed SDK and API reference generator helpers.

package apisdkgen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/maruel/apisdkgen/apispec"
)

var pathParamRe = regexp.MustCompile(`\{(\w+)\}`)

// swiftReservedWords is the set of Swift keywords that require backtick escaping
// when used as property names.
var swiftReservedWords = map[string]struct{}{
	"init": {}, "deinit": {}, "class": {}, "struct": {}, "enum": {},
	"extension": {}, "protocol": {}, "var": {}, "let": {}, "func": {},
	"return": {}, "if": {}, "else": {}, "switch": {}, "case": {},
	"default": {}, "for": {}, "in": {}, "while": {}, "repeat": {},
	"do": {}, "try": {}, "catch": {}, "throw": {}, "throws": {},
	"import": {}, "typealias": {}, "where": {}, "guard": {},
	"defer": {}, "break": {}, "continue": {}, "fallthrough": {},
	"as": {}, "is": {}, "nil": {}, "true": {}, "false": {},
	"self": {}, "Self": {}, "super": {}, "static": {}, "operator": {},
	"type": {},
}

// loadDocsInDir parses Go source files in dir and extracts documentation
// comments, source file tracking, and alias type definitions, then attaches the
// immutable configuration and merges its configured error-code alias.
func loadDocsInDir[C ~string](dir string, cfg *apispec.Config[C]) (*docRegistry[C], error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	reg := &docRegistry[C]{
		cfg:      cfg,
		typeDoc:  map[string]string{},
		typeFile: map[string]string{},
		fieldDoc: map[string]map[string]string{},
	}

	// Pass 1: collect struct types and string alias declarations.
	stringAliases := map[string]string{} // type name → source file
	for _, file := range files {
		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.TYPE {
				continue
			}
			for _, spec := range genDecl.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				switch t := ast.Unparen(typeSpec.Type).(type) {
				case *ast.StructType:
					var doc string
					if typeSpec.Doc != nil {
						doc = typeSpec.Doc.Text()
					} else if genDecl.Doc != nil && len(genDecl.Specs) == 1 {
						doc = genDecl.Doc.Text()
					}
					fn := filepath.Base(fset.Position(typeSpec.Pos()).Filename)
					reg.typeFile[typeSpec.Name.Name] = fn
					if doc = strings.TrimSpace(doc); doc != "" {
						reg.typeDoc[typeSpec.Name.Name] = doc
					}
					fieldDocs := map[string]string{}
					for _, field := range t.Fields.List {
						var fdoc string
						if field.Doc != nil {
							fdoc = strings.TrimSpace(field.Doc.Text())
						} else if field.Comment != nil {
							fdoc = strings.TrimSpace(field.Comment.Text())
						}
						if fdoc != "" {
							for _, name := range field.Names {
								fieldDocs[name.Name] = fdoc
							}
						}
					}
					if len(fieldDocs) > 0 {
						reg.fieldDoc[typeSpec.Name.Name] = fieldDocs
					}
				case *ast.Ident:
					if t.Name == "string" {
						fn := filepath.Base(fset.Position(typeSpec.Pos()).Filename)
						stringAliases[typeSpec.Name.Name] = fn
						reg.typeFile[typeSpec.Name.Name] = fn
						var doc string
						if typeSpec.Doc != nil {
							doc = typeSpec.Doc.Text()
						} else if genDecl.Doc != nil && len(genDecl.Specs) == 1 {
							doc = genDecl.Doc.Text()
						}
						if doc = strings.TrimSpace(doc); doc != "" {
							reg.typeDoc[typeSpec.Name.Name] = doc
						}
					}
				case *ast.ArrayType, *ast.MapType:
					// Named slice type.
					fn := filepath.Base(fset.Position(typeSpec.Pos()).Filename)
					reg.typeFile[typeSpec.Name.Name] = fn
					var doc string
					if typeSpec.Doc != nil {
						doc = typeSpec.Doc.Text()
					} else if genDecl.Doc != nil && len(genDecl.Specs) == 1 {
						doc = genDecl.Doc.Text()
					}
					if doc = strings.TrimSpace(doc); doc != "" {
						reg.typeDoc[typeSpec.Name.Name] = doc
					}
				}
			}
		}
	}

	// Resolve only constants belonging to discovered string aliases. Other
	// declarations remain available as references without requiring a complete
	// type-checkable consumer package.
	resolver := stringConstResolver{aliases: stringAliases, declarations: map[string]stringConstDecl{}, values: map[string]stringConstValue{}, active: map[string]struct{}{}}
	var names []string
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			var prior *ast.ValueSpec
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				value := vs
				if len(vs.Values) == 0 {
					value = prior
				} else {
					prior = vs
				}
				if value == nil || len(value.Values) != len(vs.Names) {
					return nil, fmt.Errorf("invalid constant declaration at %s", fset.Position(vs.Pos()))
				}
				for i, name := range vs.Names {
					if name.Name == "_" {
						continue
					}
					resolver.declarations[name.Name] = stringConstDecl{expr: value.Values[i], typ: value.Type, doc: valueSpecDoc(vs)}
					names = append(names, name.Name)
				}
			}
		}
	}
	aliasConsts := map[string][]aliasConstant{}
	for _, name := range names {
		if resolver.constantType(name, map[string]struct{}{}) == "" {
			continue
		}
		v, err := resolver.resolve(name)
		if err != nil {
			return nil, fmt.Errorf("string constant %s: %w", name, err)
		}
		aliasConsts[v.typeName] = append(aliasConsts[v.typeName], aliasConstant{name: name, value: v.value, doc: resolver.declarations[name].doc})
	}

	// Build aliases in sorted order for deterministic output.
	aliasNames := slices.Sorted(maps.Keys(stringAliases))
	for _, name := range aliasNames {
		file := stringAliases[name]
		reg.aliases = append(reg.aliases, aliasInfo{
			name:      name,
			file:      file,
			constants: aliasConsts[name],
		})
	}

	reg.aliasNames = map[string]struct{}{}
	for _, a := range reg.aliases {
		reg.aliasNames[a.name] = struct{}{}
	}

	if err := reg.addConfiguredErrorCodeAlias(); err != nil {
		return nil, fmt.Errorf("configuring error-code type: %w", err)
	}
	return reg, nil
}

// String enum extraction supports literals, local references, parentheses,
// concatenation, string min/max, string/named-string conversions, and inherited const specs.
// Imported expressions and numeric conversions require a consumer-provided
// literal declaration instead of silently producing incomplete enum metadata.
type stringConstDecl struct {
	expr ast.Expr
	typ  ast.Expr
	doc  string
}

type stringConstValue struct {
	value    string
	typeName string
}

// mergeType preserves the named type carried by supported string operands.
func (v *stringConstValue) mergeType(other stringConstValue) error {
	if v.typeName != "" && other.typeName != "" && v.typeName != other.typeName {
		return fmt.Errorf("incompatible string types %s and %s", v.typeName, other.typeName)
	}
	if v.typeName == "" {
		v.typeName = other.typeName
	}
	return nil
}

type stringConstResolver struct {
	aliases      map[string]string
	declarations map[string]stringConstDecl
	values       map[string]stringConstValue
	active       map[string]struct{}
}

func (r *stringConstResolver) constantType(name string, visiting map[string]struct{}) string {
	if _, ok := visiting[name]; ok {
		return ""
	}
	visiting[name] = struct{}{}
	defer delete(visiting, name)
	d, ok := r.declarations[name]
	if !ok {
		return ""
	}
	if d.typ != nil {
		if ident, ok := ast.Unparen(d.typ).(*ast.Ident); ok {
			if _, ok := r.aliases[ident.Name]; ok {
				return ident.Name
			}
		}
		return ""
	}
	return r.expressionType(d.expr, visiting)
}

// expressionType recognizes the operators and calls that can retain a named
// string result. Comparisons and other constant builtins have nonstring results.
func (r *stringConstResolver) expressionType(expr ast.Expr, visiting map[string]struct{}) string {
	switch e := ast.Unparen(expr).(type) {
	case *ast.Ident:
		return r.constantType(e.Name, visiting)
	case *ast.BinaryExpr:
		// Concatenation retains a named string type; comparisons yield bool.
		if e.Op == token.ADD {
			if name := r.expressionType(e.X, visiting); name != "" {
				return name
			}
			return r.expressionType(e.Y, visiting)
		}
	case *ast.CallExpr:
		if ident, ok := ast.Unparen(e.Fun).(*ast.Ident); ok {
			if _, ok := r.aliases[ident.Name]; ok {
				return ident.Name
			}
			// min/max retain a named argument type, even when it is not first.
			// Other constant builtins return unnamed strings, bools, or numbers.
			if ident.Name == "min" || ident.Name == "max" {
				for _, arg := range e.Args {
					if name := r.expressionType(arg, visiting); name != "" {
						return name
					}
				}
			}
		}
	}
	return ""
}

func (r *stringConstResolver) resolve(name string) (stringConstValue, error) {
	if v, ok := r.values[name]; ok {
		return v, nil
	}
	if _, ok := r.active[name]; ok {
		return stringConstValue{}, fmt.Errorf("cyclic reference to %s", name)
	}
	d, ok := r.declarations[name]
	if !ok {
		return stringConstValue{}, fmt.Errorf("unknown reference %s", name)
	}
	r.active[name] = struct{}{}
	defer delete(r.active, name)
	v, err := r.expression(d.expr)
	if err != nil {
		return stringConstValue{}, err
	}
	if d.typ != nil {
		ident, ok := ast.Unparen(d.typ).(*ast.Ident)
		if !ok {
			return stringConstValue{}, fmt.Errorf("unsupported type expression %T", d.typ)
		}
		if _, ok := r.aliases[ident.Name]; !ok && ident.Name != "string" {
			return stringConstValue{}, fmt.Errorf("unsupported type %s", ident.Name)
		}
		if v.typeName != "" && v.typeName != ident.Name {
			return stringConstValue{}, fmt.Errorf("incompatible string types %s and %s", ident.Name, v.typeName)
		}
		v.typeName = ident.Name
		if v.typeName == "string" {
			v.typeName = ""
		}
	}
	r.values[name] = v
	return v, nil
}

func (r *stringConstResolver) expression(expr ast.Expr) (stringConstValue, error) {
	switch e := ast.Unparen(expr).(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			v, err := strconv.Unquote(e.Value)
			return stringConstValue{value: v}, err
		}
	case *ast.Ident:
		return r.resolve(e.Name)
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			a, err := r.expression(e.X)
			if err != nil {
				return stringConstValue{}, err
			}
			b, err := r.expression(e.Y)
			if err != nil {
				return stringConstValue{}, err
			}
			if err := a.mergeType(b); err != nil {
				return stringConstValue{}, err
			}
			a.value += b.value
			return a, nil
		}
	case *ast.CallExpr:
		if ident, ok := ast.Unparen(e.Fun).(*ast.Ident); ok {
			_, alias := r.aliases[ident.Name]
			if (alias || ident.Name == "string") && len(e.Args) == 1 {
				v, err := r.expression(e.Args[0])
				if alias {
					v.typeName = ident.Name
				} else {
					v.typeName = ""
				}
				return v, err
			}
			if !alias && (ident.Name == "min" || ident.Name == "max") && len(e.Args) > 0 {
				best, err := r.expression(e.Args[0])
				if err != nil {
					return stringConstValue{}, err
				}
				for _, arg := range e.Args[1:] {
					next, err := r.expression(arg)
					if err != nil {
						return stringConstValue{}, err
					}
					if err := best.mergeType(next); err != nil {
						return stringConstValue{}, err
					}
					if ident.Name == "min" {
						best.value = min(best.value, next.value)
					} else {
						best.value = max(best.value, next.value)
					}
				}
				return best, nil
			}
		}
	}
	return stringConstValue{}, fmt.Errorf("unsupported expression %T", expr)
}

func valueSpecDoc(vs *ast.ValueSpec) string {
	if vs.Doc != nil {
		return strings.TrimSpace(vs.Doc.Text())
	}
	if vs.Comment != nil {
		return strings.TrimSpace(vs.Comment.Text())
	}
	return ""
}

// formatBlockDoc formats a doc string as a /** ... */ block comment with the given indent.
// Returns an empty string when doc is empty.
func formatBlockDoc(doc, indent string) string {
	if doc == "" {
		return ""
	}
	lines := strings.Split(doc, "\n")
	// Drop trailing empty lines.
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return ""
	}
	if len(lines) == 1 {
		return indent + "/** " + lines[0] + " */\n"
	}
	var b strings.Builder
	b.WriteString(indent + "/**\n")
	for _, l := range lines {
		if l == "" {
			b.WriteString(indent + " *\n")
		} else {
			b.WriteString(indent + " * " + l + "\n")
		}
	}
	b.WriteString(indent + " */\n")
	return b.String()
}

// formatSwiftDoc formats a doc string as Swift triple-slash documentation comments.
// Returns an empty string when doc is empty.
func formatSwiftDoc(doc, indent string) string {
	if doc == "" {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(doc), "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	for _, l := range lines {
		if l == "" {
			b.WriteString(indent + "///\n")
		} else {
			b.WriteString(indent + "/// " + l + "\n")
		}
	}
	return b.String()
}

// aliasInfo describes a Go named-string type and its constant values,
// extracted from Go source files by loadDocs.
type aliasInfo struct {
	name      string          // e.g. "Harness"
	file      string          // source filename, e.g. "types.go"
	constants []aliasConstant // enum values
}

// shortName returns the const name with the type prefix stripped
// (e.g. "HarnessClaude" → "Claude"). Used for Kotlin/Swift.
func (a aliasInfo) shortName(c aliasConstant) string { return strings.TrimPrefix(c.name, a.name) }

// aliasConstant is a single enum value for a string type alias.
type aliasConstant struct {
	name  string // const name from Go source, e.g. "HarnessClaude"
	value string // wire value, e.g. "claude"
	doc   string // source documentation, when present
}
