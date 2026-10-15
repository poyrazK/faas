package appstandards

import (
	"strings"
	"testing"
)

func TestParseLocalSettings(t *testing.T) {
	for _, raw := range []string{
		`null`, `[]`, `{"unknown":true}`, `{"require_signed":null}`,
		`{"require_signed":true,"require_signed":false}`, `{"security_policy":"invalid"}`,
		`{"egress_cidrs":["not-a-prefix"]}`, `{"egress_extra_ports":[25.5]}`,
		`{"trusted_publishers":["00000000-0000-0000-0000-000000000000"]}`,
		`{} {}`, strings.Repeat(" ", testLimits.DefinitionBytes+1),
	} {
		if _, err := ParseSettings([]byte(raw), testLimits); err == nil {
			t.Fatalf("accepted invalid local settings %q", raw)
		}
	}
	settings, err := ParseSettings([]byte(`{}`), testLimits)
	if err != nil || settings == nil || len(settings) != 0 {
		t.Fatalf("clear local settings: %+v %v", settings, err)
	}
	settings, err = ParseSettings([]byte(`{"egress_cidrs":["8.8.8.8/24","8.8.8.0/24"],"egress_extra_ports":[9443,8443,8443]}`), testLimits)
	if err != nil || string(settings[EgressCIDRs]) != `["8.8.8.0/24"]` || string(settings[EgressExtraPorts]) != `[8443,9443]` {
		t.Fatalf("canonical settings: %+v %v", settings, err)
	}
}
