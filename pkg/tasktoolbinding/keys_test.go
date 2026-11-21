package tasktoolbinding_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/tasktoolbinding"
)

func TestConfiguredBindingKeys(t *testing.T) {
	key := strings.Repeat("x", 32)
	if !tasktoolbinding.OwnerKeysMatch(key, key) || tasktoolbinding.OwnerKeysMatch(key, strings.Repeat("y", 32)) || tasktoolbinding.OwnerKeysMatch("short", "short") {
		t.Fatal("owner key agreement")
	}
	a := `{"activeKeyId":"current","keys":{"current":"` + key + `","old":"` + strings.Repeat("o", 32) + `"}}`
	b := `{"keys":{"old":"` + strings.Repeat("o", 32) + `","current":"` + key + `"},"activeKeyId":"current"}`
	if !tasktoolbinding.PayloadKeysMatch(a, b) {
		t.Fatal("valid canonical ring rejected")
	}
	for _, bad := range []string{
		strings.Replace(a, "current\",\"keys", "missing\",\"keys", 1),
		strings.Replace(a, key, "short", 1),
		strings.Replace(a, "current", "bad.key", -1),
		strings.TrimSuffix(a, "}") + `,"extra":true}`,
		a + " trailing",
		a + " {}",
		`{"activeKeyId":"legacy","keys":{}}`,
		`{"activeKeyId":"legacy","keys":{"legacy":"` + key + `"}}`,
	} {
		if tasktoolbinding.PayloadKeysMatch(a, bad) {
			t.Fatal("invalid or changed configured key ring passed")
		}
	}
}
