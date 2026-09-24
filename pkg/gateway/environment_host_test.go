package gateway

import (
	"strings"
	"testing"
)

func TestEnvironmentHostRoundTripAndStrictShape(t *testing.T) {
	const environmentID = "89c8fd3e-e348-4c81-bf37-a8a88a662d2e"
	const appID = "6c0ea4d2-924d-4b88-af80-63c0e84b7de5"
	host := BuildEnvironmentHost(".gregale.dev", environmentID, appID)
	if compact := BuildEnvironmentHost(".gregale.dev", strings.ReplaceAll(environmentID, "-", ""), strings.ReplaceAll(appID, "-", "")); compact != host {
		t.Fatalf("compact ID host = %q, want %q", compact, host)
	}
	if len(strings.TrimSuffix(host, ".gregale.dev")) != 57 {
		t.Fatalf("host = %q, want one 57-byte label", host)
	}
	gotEnvironment, gotApp, ok := EnvironmentIDsFromHost(".gregale.dev", host)
	if !ok || gotEnvironment != environmentID || gotApp != appID {
		t.Fatalf("parsed host = %q %q %v", gotEnvironment, gotApp, ok)
	}
	for _, invalid := range []string{
		strings.ToUpper(host),
		strings.ToUpper(strings.TrimSuffix(host, ".gregale.dev")) + ".gregale.dev",
		"x." + host,
		strings.Replace(host, "env-", "deploy-", 1),
		strings.Replace(host, ".gregale.dev", ".example.dev", 1),
		host[:len(host)-len(".gregale.dev")-1] + ".gregale.dev",
	} {
		if _, _, ok := EnvironmentIDsFromHost(".gregale.dev", invalid); ok {
			t.Fatalf("accepted malformed environment host %q", invalid)
		}
	}
	if BuildEnvironmentHost("", environmentID, appID) != "" || BuildEnvironmentHost(".gregale.dev", "not-a-uuid", appID) != "" {
		t.Fatal("built a host without a configured suffix or valid identities")
	}
}
