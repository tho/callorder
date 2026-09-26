// Package plugin registers callorder with golangci-lint's module plugin system.
package plugin

import (
	"fmt"
	"strconv"

	"github.com/golangci/plugin-module-register/register"
	"github.com/tho/callorder"
	"golang.org/x/tools/go/analysis"
)

func init() {
	register.Plugin("callorder", func(settings any) (register.LinterPlugin, error) {
		checks, err := register.DecodeSettings[map[string]bool](settings)
		if err != nil {
			return nil, err
		}

		// Settings are the analyzer's flags, so both share names and defaults.
		analyzer := callorder.NewAnalyzer()
		for name, enabled := range checks {
			if analyzer.Flags.Lookup(name) == nil {
				return nil, fmt.Errorf("unknown callorder setting %q", name)
			}

			if err := analyzer.Flags.Set(name, strconv.FormatBool(enabled)); err != nil {
				return nil, err
			}
		}

		return linter{analyzer}, nil
	})
}

type linter struct {
	analyzer *analysis.Analyzer
}

func (l linter) BuildAnalyzers() ([]*analysis.Analyzer, error) {
	return []*analysis.Analyzer{l.analyzer}, nil
}

func (linter) GetLoadMode() string {
	return register.LoadModeTypesInfo
}
