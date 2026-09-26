package plugin

import (
	"testing"

	"github.com/golangci/plugin-module-register/register"
)

func TestRegistration(t *testing.T) {
	newPlugin, err := register.GetPlugin("callorder")
	if err != nil {
		t.Fatal(err)
	}

	plugin, err := newPlugin(nil)
	if err != nil {
		t.Fatal(err)
	}

	analyzers, err := plugin.BuildAnalyzers()
	if err != nil || len(analyzers) != 1 || analyzers[0].Name != "callorder" ||
		plugin.GetLoadMode() != register.LoadModeTypesInfo {
		t.Fatalf("invalid plugin: analyzers=%v, load mode=%q, error=%v", analyzers, plugin.GetLoadMode(), err)
	}

	if got := analyzers[0].Flags.Lookup("init-first").Value.String(); got != "false" {
		t.Fatalf("init-first default = %s, want false", got)
	}

	configured, err := newPlugin(map[string]any{"init-first": true, "constructor": false})
	if err != nil {
		t.Fatal(err)
	}

	analyzers, err = configured.BuildAnalyzers()
	if err != nil || analyzers[0].Flags.Lookup("init-first").Value.String() != "true" ||
		analyzers[0].Flags.Lookup("constructor").Value.String() != "false" {
		t.Fatalf("settings not applied: error=%v", err)
	}

	for _, settings := range []map[string]any{{"unknown": true}, {"function": "yes"}} {
		if _, err := newPlugin(settings); err == nil {
			t.Fatalf("settings %v should fail", settings)
		}
	}
}
