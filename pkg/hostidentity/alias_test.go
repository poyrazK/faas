// adr: 570
package hostidentity

import (
	"strings"
	"testing"
)

func TestDeploymentAliasLabelAndNamespace(t *testing.T) {
	id := "01234567-89ab-4cde-8123-456789abcdef"
	label, ok := DeploymentAliasLabel(strings.ToUpper(id), "candidate")
	if !ok || label != "tag-candidate-0123456789ab4cde8123456789abcdef" {
		t.Fatalf("canonical alias label=%q valid=%v", label, ok)
	}
	for _, test := range []struct {
		suffix, host string
		want         bool
	}{
		{".apps.example.test", label + ".apps.example.test", true},
		{".gregale.dev", label + ".apps.example.test", false},
		{"", label + ".apps.example.test", false},
		{".apps.example.test", "inner." + label + ".apps.example.test", false},
		{".apps.example.test", "web.apps.example.test", false},
	} {
		got, matched := DeploymentAliasLabelFromHost(test.suffix, test.host)
		if matched != test.want || matched && got != label {
			t.Fatalf("host=%q label=%q match=%v want=%v", test.host, got, matched, test.want)
		}
	}
	if _, ok := DeploymentAliasLabel("not-a-uuid", "candidate"); ok {
		t.Fatal("invalid app identity acquired an alias label")
	}
}
