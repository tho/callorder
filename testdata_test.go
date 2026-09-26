package callorder

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	funcorder "github.com/manuelarte/funcorder/analyzer"
	"gitlab.com/bosi/decorder"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/analysis/checker"
	"golang.org/x/tools/go/packages"
)

// TestFixtures checks the diagnostics and fixes for testdata/src, then checks
// that the fixed files satisfy callorder again, and funcorder and decorder with
// the same settings, at the versions golangci-lint pins.
func TestFixtures(t *testing.T) {
	fixtures := []struct {
		dir   string
		flags []string
		// remaining lists funcorder reports that only moving a type could fix.
		remaining []string
	}{
		{dir: "calls"},
		{dir: "funcorder"},
		{dir: "initcaller"},
		{dir: "initfirst", flags: []string{"init-first=true"}},
		{dir: "alphabetical", flags: []string{"alphabetical=true"}},
		{dir: "function", flags: []string{"function=true"}},
		{dir: "disabled", flags: []string{"constructor=false", "struct-method=false"}},
		{dir: "helperabovetype"},
		{dir: "constructorabovetype", remaining: []string{"should be placed after the struct declaration"}},
		{dir: "generated"},
	}

	// decorder has one global analyzer; only its init-first check concerns functions.
	setFlag(t, decorder.Analyzer, "disable-dec-num-check", "true")
	setFlag(t, decorder.Analyzer, "disable-dec-order-check", "true")

	for _, fixture := range fixtures {
		t.Run(fixture.dir, func(t *testing.T) {
			callorder, funcorder := NewAnalyzer(), funcorder.NewAnalyzer()
			for _, flag := range fixture.flags {
				name, value, _ := strings.Cut(flag, "=")
				setFlag(t, callorder, name, value)
			}

			for _, name := range []string{"constructor", "struct-method", "alphabetical", "function"} {
				setFlag(t, funcorder, name, callorder.Flags.Lookup(name).Value.String())
			}

			initFirst, _ := strconv.ParseBool(callorder.Flags.Lookup("init-first").Value.String())
			setFlag(t, decorder.Analyzer, "disable-init-func-first-check", strconv.FormatBool(!initFirst))

			analysistest.RunWithSuggestedFixes(t, analysistest.TestData(), callorder, fixture.dir)

			diagnostics := fixedDiagnostics(t, fixture.dir, callorder, funcorder, decorder.Analyzer)
			for _, diagnostic := range diagnostics {
				if !slices.ContainsFunc(fixture.remaining, func(want string) bool { return strings.Contains(diagnostic, want) }) {
					t.Errorf("fixed files: %s", diagnostic)
				}
			}

			for _, want := range fixture.remaining {
				if !slices.ContainsFunc(diagnostics, func(diagnostic string) bool { return strings.Contains(diagnostic, want) }) {
					t.Errorf("fixed files: missing %q", want)
				}
			}
		})
	}
}

func setFlag(t *testing.T, analyzer *analysis.Analyzer, name, value string) {
	t.Helper()

	if err := analyzer.Flags.Set(name, value); err != nil {
		t.Fatal(err)
	}
}

// fixedDiagnostics loads a fixture with its golden files in place of the
// originals, and returns the analyzers' diagnostics as "analyzer: message".
func fixedDiagnostics(t *testing.T, dir string, analyzers ...*analysis.Analyzer) []string {
	t.Helper()

	module := t.TempDir()
	if err := os.WriteFile(filepath.Join(module, "go.mod"), []byte("module fixture\n\ngo 1.27\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	paths, err := filepath.Glob(filepath.Join("testdata", "src", dir, "*.go"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no fixture files: %v", err)
	}

	for _, path := range paths {
		content, err := os.ReadFile(path + ".golden")
		if errors.Is(err, fs.ErrNotExist) {
			content, err = os.ReadFile(path)
		}

		if err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(module, filepath.Base(path)), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	pkgs, err := packages.Load(&packages.Config{Mode: packages.LoadAllSyntax, Dir: module}, ".")
	if err != nil || packages.PrintErrors(pkgs) > 0 {
		t.Fatalf("load fixed %s: %v", dir, err)
	}

	graph, err := checker.Analyze(analyzers, pkgs, nil)
	if err != nil {
		t.Fatal(err)
	}

	diagnostics := []string{}

	for _, action := range graph.Roots {
		if action.Err != nil {
			t.Fatal(action.Err)
		}

		for _, diagnostic := range action.Diagnostics {
			diagnostics = append(diagnostics, action.Analyzer.Name+": "+diagnostic.Message)
		}
	}

	return diagnostics
}
