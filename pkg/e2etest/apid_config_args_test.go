package e2etest

import (
	"reflect"
	"testing"
)

func TestHarnessAPIDConfigSurvivesLaunchRecipe(t *testing.T) {
	env := []string{"FAAS_E2E_APID_CONFIG=/tmp/old.toml", "FAAS_E2E_APID_CONFIG=/tmp/isolated config.toml"}
	if got := daemonConfigArgs("apid", env); !reflect.DeepEqual(got, []string{"--config", "/tmp/isolated config.toml"}) {
		t.Fatal("operator config did not follow the retained launch recipe", got)
	}
	for _, name := range []string{"schedd", "vmmd", "imaged"} {
		if got := daemonConfigArgs(name, env); len(got) != 0 {
			t.Fatal("API config leaked to another daemon", name, got)
		}
	}
	if got := daemonConfigArgs("apid", nil); len(got) != 0 {
		t.Fatal("ordinary harness startup changed", got)
	}
}
