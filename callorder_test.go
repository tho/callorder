package callorder

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
)

func TestAnalyzer(t *testing.T) {
	tests := []struct {
		name    string
		flags   []string
		sources []string
		want    []int
		message string
	}{
		{
			name: "top down",
			sources: []string{`package p
func Entry() { middle() }
func middle() { leaf() }
func leaf() {}`},
		},
		{
			name: "all direct callers, repeated calls deduplicated",
			sources: []string{`package p
func helper() {}
func First() { helper(); helper() }
func Second() { helper() }`},
			want: []int{1, 2, 0},
		},
		{
			name: "exported callees exempt",
			sources: []string{`package p
type T struct{}
func Exported() {}
func (T) Method() {}
func caller() { Exported(); T{}.Method() }`},
		},
		{
			name: "methods, expressions, promotion and receiver identity",
			sources: []string{`package p
type T struct{}
type U struct{ T }
type V struct{}
func (*T) helper() {}
func Caller() { t := &T{}; t.helper(); (*T).helper(t); u := &U{}; u.helper(); V{}.helper() }
func (V) helper() {}`},
			want: []int{1, 0, 2},
		},
		{
			name: "generic functions and instantiated methods",
			sources: []string{`package p
type T[A any] struct{}
func one[A any](A) {}
func two[A, B any](A, B) {}
func (T[A]) helper() {}
func Caller() { one(1); (one[int])(1); two[int, string](1, ""); T[int]{}.helper(); T[string].helper(T[string]{}) }`},
			want: []int{3, 0, 1, 2},
		},
		{
			name: "interface dispatch and function values ignored",
			sources: []string{`package p
type I interface{ helper() }
type T struct{}
type Box struct{ f func() }
func helper() {}
func (T) helper() {}
func Caller(i I) {
 i.helper(); I.helper(i)
 f := helper; f()
 m := T{}.helper; m()
 fs := []func(){helper}; fs[0]()
 b := Box{f: helper}; b.f()
 helper := f; helper()
}
func Generic[A I](a A) { a.helper() }`},
		},
		{
			name: "direct calls in closures, defer, go, and parentheses",
			sources: []string{`package p
func helper() {}
func Caller() {
 f := func() { helper() }; f()
 defer (helper)()
 go helper()
}`},
			want: []int{1, 0},
		},
		{
			name: "same file only",
			sources: []string{
				"package p; func helper() { Caller() }",
				"package p; func Caller() { helper() }",
			},
		},
		{
			name:    "self recursion",
			sources: []string{"package p; func recursive() { recursive() }"},
		},
		{
			name: "adjacent mutual recursion",
			sources: []string{`package p
func Entry() { a() }
func a() { b() }
func b() { c() }
func c() { a() }`},
		},
		{
			name: "separated recursive group",
			sources: []string{`package p
func a() { b() }
func unrelated() {}
func b() { a() }`},
			want: []int{0, 2, 1},
		},
		{
			name: "recursion through exported method",
			sources: []string{`package p
type T struct{}
func (t T) helper() { t.Exported() }
func (t T) Exported() { t.helper() }`},
			want: []int{1, 0},
		},
		{
			name: "funcorder exported methods precede private methods in mixed groups",
			sources: []string{`package p
type T struct{}
func (t T) First() { t.first() }
func (t T) Second() { t.second() }
func (t T) first() { t.First() }
func (t T) second() { t.Second() }`},
		},
		{
			name: "funcorder constructors precede methods in mixed groups",
			sources: []string{`package p
type T struct{}
func NewFirst() *T { t := &T{}; t.first(); return t }
func MustSecond() *T { t := &T{}; t.second(); return t }
func (T) first() { NewFirst() }
func (T) second() { MustSecond() }`},
		},
		{
			name: "New prefix returning a builtin is not a funcorder constructor",
			sources: []string{`package p
type T struct{}
func NewCount() int { T{}.helper(); return 0 }
func unrelated() {}
func (T) helper() { NewCount() }`},
			want: []int{0, 2, 1},
		},
		{
			name: "empty result list is not a funcorder constructor",
			sources: []string{`package p
func NewAction() () { helper() }
func unrelated() {}
func helper() { NewAction() }`},
			want: []int{0, 2, 1},
		},
		{
			name: "bare New is not a funcorder constructor",
			sources: []string{`package p
type T struct{}
func New() T { T{}.helper(); return T{} }
func unrelated() {}
func (T) helper() { New() }`},
			want: []int{0, 2, 1},
		},
		{
			name: "private recursive methods still need adjacency",
			sources: []string{`package p
type T struct{}
func (t T) a() { t.b() }
func (T) unrelated() {}
func (t T) b() { t.a() }`},
			want: []int{0, 2, 1},
		},
		{
			name: "external edges of recursive groups still checked",
			sources: []string{`package p
func leaf() {}
func a() { b(); leaf() }
func b() { a() }
func Caller() { b() }`},
			want: []int{3, 1, 2, 0},
		},
		{
			name: "separate recursive groups",
			sources: []string{`package p
func a() { b() }
func b() { a() }
func c() { d(); a() }
func d() { c() }`},
			want: []int{2, 3, 0, 1},
		},
		{
			name: "non-function declarations do not split a group",
			sources: []string{`package p
func a() { b() }
type T struct{}
func b() { a() }`},
		},
		{
			name: "init is a caller",
			sources: []string{`package p
func helper() {}
func init() { helper() }
func init() { helper() }`},
			want: []int{1, 2, 0},
		},
		{
			name: "no function bodies",
			sources: []string{`package p
type T int
func external()`},
		},
		{
			name:    "no functions",
			sources: []string{"package p; type T int"},
		},
		{
			name: "exclusive helpers follow caller in first-use order",
			sources: []string{`package p
func search() { priceQuery(); decodeSearch() }
func fetchBreakdown() { decodeBreakdown() }
func decodeSearch() {}
func priceQuery() {}
func decodeBreakdown() {}`},
			want: []int{0, 3, 2, 1, 4},
		},
		{
			name: "nested helpers before siblings",
			sources: []string{`package p
func Entry() { first(); second() }
func first() { leaf() }
func second() {}
func leaf() {}`},
			want: []int{0, 1, 3, 2},
		},
		{
			name: "shared helper follows last caller in call order",
			sources: []string{`package p
func First() { shared(); own() }
func Second() { other(); shared() }
func shared() {}
func own() {}
func other() {}`},
			want: []int{0, 3, 1, 4, 2},
		},
		{
			name:    "all init functions first with execution order preserved",
			flags:   []string{"init-first=true"},
			message: "init functions come first (decorder init-first)",
			sources: []string{`package p
var x int
func init() { x = 1; first() }
func first() {}
func init() { x = 2; second() }
func second() {}`},
			want: []int{0, 2, 1, 3},
		},
		{
			name: "constructors before methods without recursion",
			sources: []string{`package p
type T struct{}
func NewFirst() *T { t := &T{}; t.first(); return t }
func NewSecond() *T { t := &T{}; t.second(); return t }
func (T) first() {}
func (T) second() {}`},
		},
		{
			name: "exported methods before private methods without recursion",
			sources: []string{`package p
type T struct{}
func (t T) First() { t.first() }
func (t T) Second() { t.second() }
func (T) first() {}
func (T) second() {}`},
		},
		{
			name: "helper above type moves below unrelated function",
			sources: []string{`package p
func helper() int { return 1 }
func unrelated() {}
type T struct{ n int }
func NewT() *T { return &T{n: helper()} }`},
			want: []int{1, 0, 2},
		},
		{
			name: "lone constructor above its type stays there",
			sources: []string{`package p
func NewT() *T { return &T{} }
type T struct{}`},
		},
		{
			name:  "constructors above their type still sorted",
			flags: []string{"alphabetical=true"},
			sources: []string{`package p
func NewB() *T { return &T{} }
func NewA() *T { return &T{} }
type T struct{}`},
			want:    []int{1, 0},
			message: "(funcorder alphabetical)",
		},
		{
			name: "constructor moved below its type when possible",
			sources: []string{`package p
func NewT() *T { return &T{} }
type T struct{}
func other() {}`},
			want: []int{1, 0},
		},
		{
			name: "method above its type with constructor below is left to funcorder",
			sources: []string{`package p
func (T) M() {}
type T struct{}
func NewT() T { return T{} }`},
		},
		{
			name: "type parameters are not constructor types",
			sources: []string{`package p
func Zero[X any]() X { var x X; return x }
func NewZero[X any]() X { return Zero[X]() }`},
		},
		{
			name: "declarations sharing a line leave trailing comments in place",
			sources: []string{`package p
func helper() {}; var x = 1 // x
func Other() {}; func Entry() { helper() } // entry`},
			want: []int{1, 2, 0},
		},
		{
			name:  "alphabetical through a chain",
			flags: []string{"alphabetical=true"},
			sources: []string{`package p
type T struct{}
func (T) C() {}
func (T) B() {}
func (T) A() {}`},
			want:    []int{2, 1, 0},
			message: "expected A before C: constructors and methods are sorted by name (funcorder alphabetical)",
		},
		{
			name: "calls order methods without alphabetical",
			sources: []string{`package p
type T struct{}
func (t T) a() {}
func (t T) b() { t.a() }`},
			want:    []int{1, 0},
			message: "expected b before a in depth-first call order",
		},
		{
			name:  "alphabetical constructors only with the constructor check",
			flags: []string{"alphabetical=true", "constructor=false"},
			sources: []string{`package p
type T struct{}
func NewB() T { return T{} }
func NewA() T { return T{} }`},
		},
		{
			name:  "function check excludes init and methods",
			flags: []string{"function=true"},
			sources: []string{`package p
type T struct{}
func init() {}
func (T) m() {}
func Exported() {}`},
		},
		{
			name:  "function violation needing a type move is left to funcorder",
			flags: []string{"function=true"},
			sources: []string{`package p
func helper() {}
type T struct{}
func NewT() T { return T{} }`},
		},
		{
			name: "constructor stays after intervening type declaration",
			sources: []string{`package p
func root() {}
type T struct{}
func NewThing() T { return T{} }`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages, fixed, originalFuncs := analyzeSources(t, tt.sources, tt.flags...)
			if tt.want == nil {
				if len(messages) != 0 {
					t.Fatalf("unexpected diagnostics: %v", messages)
				}

				return
			}

			if len(messages) != 1 || !strings.Contains(messages[0], tt.message) {
				t.Fatalf("expected one diagnostic containing %q, got %v", tt.message, messages)
			}

			again, _, fixedFuncs := analyzeSources(t, fixed, tt.flags...)
			if len(again) != 0 {
				t.Fatalf("fix is not idempotent: %v\n%s", again, fixed[0])
			}

			want := make([]string, len(tt.want))
			for i, index := range tt.want {
				want[i] = originalFuncs[0][index]
			}

			if !slices.Equal(fixedFuncs[0], want) {
				t.Errorf("fixed functions = %v, want %v", fixedFuncs[0], want)
			}
		})
	}
}

func TestCommand(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "callorder")

	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/callorder")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build analyzer: %v\n%s", err, out)
	}

	source, err := format.Source([]byte(`// Package p tests comment and declaration preservation.
package p

type Thing struct{}
const label = "keep"
var sequence = ""

func init() { sequence += "first" }
func init() { sequence += "second" }
func Entry() { helper() }
func Other() {}

// helper documents the helper.
//go:noinline
func helper() {
 // inside helper
 _ = label
} // trailing helper

func NewFirst() *Thing { t := &Thing{}; t.first(); return t }
func NewSecond() *Thing { t := &Thing{}; t.second(); return t }
func (t *Thing) First() { t.first() }
func (t *Thing) Second() { t.second() }
func (*Thing) first() {}
func (*Thing) second() {}
`))
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "fixture.go")
	if writeErr := os.WriteFile(path, source, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}

	out, err := exec.CommandContext(t.Context(), binary, path).CombinedOutput()
	if err == nil || !bytes.Contains(out, []byte("expected helper before Other")) {
		t.Fatalf("default mode: error=%v, output=%s", err, out)
	}

	out, err = exec.CommandContext(t.Context(), binary, "-json", path).CombinedOutput()
	if err != nil || !json.Valid(out) || !bytes.Contains(out, []byte("expected helper before Other")) {
		t.Fatalf("JSON mode: error=%v, output=%s", err, out)
	}

	unchanged, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(unchanged, source) {
		t.Fatalf("reporting changed the file: %v", err)
	}

	if out, commandErr := exec.CommandContext(t.Context(), binary, "--fix", path).CombinedOutput(); commandErr != nil {
		t.Fatalf("fix: %v\n%s", commandErr, out)
	}

	fixed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	prefix, _, _ := strings.Cut(string(source), "func init()")
	if !bytes.HasPrefix(fixed, []byte(prefix)) {
		t.Fatal("fix changed non-function declarations or the package comment")
	}

	for _, comment := range []string{"// helper documents the helper.\n//\n//go:noinline\nfunc helper()", "// inside helper", "} // trailing helper"} {
		if strings.Count(string(fixed), comment) != 1 {
			t.Fatalf("comment lost or detached: %q\n%s", comment, fixed)
		}
	}

	messages, _, _ := analyzeSources(t, []string{string(fixed)})
	if len(messages) != 0 {
		t.Fatalf("fix left diagnostics: %v", messages)
	}

	if out, commandErr := exec.CommandContext(t.Context(), binary, path).CombinedOutput(); commandErr != nil {
		t.Fatalf("check fixed file: %v\n%s", commandErr, out)
	}

	if out, commandErr := exec.CommandContext(t.Context(), binary, "--fix", path).CombinedOutput(); commandErr != nil {
		t.Fatalf("second fix: %v\n%s", commandErr, out)
	}

	again, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(fixed, again) {
		t.Fatalf("second fix changed the file: %v", err)
	}

	out, err = exec.CommandContext(t.Context(), binary, "-function", path).CombinedOutput()
	if err == nil || !bytes.Contains(out, []byte("(funcorder function)")) {
		t.Fatalf("function flag: error=%v, output=%s", err, out)
	}
}

func TestOrderIdempotence(t *testing.T) {
	// Each declaration and the expression calling it; types and init are not callable.
	decls := [][2]string{
		{"type T struct{}", ""}, {"func init()", ""}, {"func init()", ""},
		{"func NewT() *T", "NewT()"}, {"func MustT() *T", "MustT()"},
		{"func (T) A()", "T{}.A()"}, {"func (T) B()", "T{}.B()"}, {"func (T) a()", "T{}.a()"}, {"func (T) b()", "T{}.b()"},
	}
	for i := range 6 {
		decls = append(decls, [2]string{fmt.Sprintf("func f%d()", i), fmt.Sprintf("f%d()", i)})
	}

	decls = append(decls, [2]string{"func F()", "F()"}, [2]string{"func G()", "G()"})

	for sample := range 300 {
		// Seed per sample so -run TestOrderIdempotence/N reproduces sample N.
		random := rand.New(rand.NewPCG(1, uint64(sample)))

		flags := []string{}
		for _, name := range []string{"init-first", "constructor", "struct-method", "alphabetical", "function"} {
			flags = append(flags, fmt.Sprintf("%s=%t", name, random.IntN(2) == 0))
		}

		t.Run(strconv.Itoa(sample), func(t *testing.T) {
			var source strings.Builder

			_, _ = source.WriteString("package p\n")
			for _, i := range random.Perm(len(decls)) {
				_, _ = source.WriteString(decls[i][0])
				if !strings.HasPrefix(decls[i][0], "func") {
					_, _ = source.WriteString("\n")

					continue
				}

				_, _ = source.WriteString(" {")
				for _, callee := range decls {
					if callee[1] != "" && random.IntN(5) == 0 {
						_, _ = fmt.Fprintf(&source, "%s;", callee[1])
					}
				}

				if strings.HasSuffix(decls[i][0], "*T") {
					_, _ = source.WriteString("return nil")
				}

				_, _ = source.WriteString("}\n")
			}

			_, fixed, _ := analyzeSources(t, []string{source.String()}, flags...)

			again, _, _ := analyzeSources(t, fixed, flags...)
			if len(again) != 0 {
				t.Fatalf("unstable order with %v: %v\n%s\n%s", flags, again, source.String(), fixed[0])
			}
		})
	}
}

// analyzeSources type-checks both original and fixed fixtures with the given
// name=value flags, and applies only the suggested edits in memory, just as the
// driver does when --fix is enabled.
func analyzeSources(t *testing.T, sources []string, flags ...string) (messages, fixed []string, functions [][]string) {
	t.Helper()

	fset := token.NewFileSet()
	files := []*ast.File{}
	contents := map[string][]byte{}
	indices := map[string]int{}
	functions = make([][]string, len(sources))

	for i, source := range sources {
		name := fmt.Sprintf("test%d.go", i)

		file, err := parser.ParseFile(fset, name, source, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}

		files = append(files, file)
		contents[name], indices[name] = []byte(source), i

		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				var out bytes.Buffer
				if err := format.Node(&out, fset, fn); err != nil {
					t.Fatal(err)
				}

				functions[i] = append(functions[i], out.String())
			}
		}
	}

	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	config := &types.Config{}

	pkg, err := config.Check("p", fset, files, info)
	if err != nil {
		t.Fatal(err)
	}

	messages = []string{}
	fixed = slices.Clone(sources)
	analyzer := NewAnalyzer()
	for _, flag := range flags {
		name, value, _ := strings.Cut(flag, "=")
		if err := analyzer.Flags.Set(name, value); err != nil {
			t.Fatal(err)
		}
	}

	_, err = analyzer.Run(&analysis.Pass{
		Analyzer: analyzer, Fset: fset, Files: files, Pkg: pkg, TypesInfo: info,
		ReadFile: func(name string) ([]byte, error) { return contents[name], nil },
		Report: func(d analysis.Diagnostic) {
			if !d.Pos.IsValid() || len(d.SuggestedFixes) != 1 || len(d.SuggestedFixes[0].TextEdits) == 0 {
				t.Fatalf("expected diagnostic with a fix: %+v", d)
			}

			file := fset.File(d.Pos)
			out := []byte(sources[indices[file.Name()]])
			edits := slices.Clone(d.SuggestedFixes[0].TextEdits)
			slices.SortFunc(edits, func(a, b analysis.TextEdit) int { return int(b.Pos - a.Pos) })

			end := file.Size()
			for _, edit := range edits {
				if file.Offset(edit.End) > end {
					t.Fatalf("overlapping edits: %+v", edits)
				}

				end = file.Offset(edit.Pos)
				out = slices.Concat(out[:end], edit.NewText, out[file.Offset(edit.End):])
			}

			messages = append(messages, d.Message)
			fixed[indices[file.Name()]] = string(out)
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	return messages, fixed, functions
}
