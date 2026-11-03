package dataapiusage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/credhash"
)

// adr: 20261029202403 amendment 1 — apid stores reported server key hashes
// keyed under the data-api ring once its keyed writes are on, and the
// result still validates as access settings.
func TestKeyServerKeys(t *testing.T) {
	sum := sha256.Sum256([]byte("gdsk_a"))
	config := api.DataAPIAccessConfig{Version: 1, Schemas: []string{"public"}, ServerKeys: []api.DataAPIServerKey{{Slot: "a", SHA256: hex.EncodeToString(sum[:])}}}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { credhash.Set(credhash.FamilyDataAPI, nil) })
	if out, err := keyServerKeys(raw); err != nil || string(out) != string(raw) {
		t.Fatalf("plain writes changed the settings: %s, %v", out, err)
	}

	line, err := credhash.NewKeyLine(1)
	if err != nil {
		t.Fatal(err)
	}
	ring, err := credhash.ParseRing([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	policy := &credhash.Policy{Ring: ring, KeyedWrites: true}
	credhash.Set(credhash.FamilyDataAPI, policy)
	out, err := keyServerKeys(raw)
	if err != nil {
		t.Fatal(err)
	}
	var keyed api.DataAPIAccessConfig
	if err := json.Unmarshal(out, &keyed); err != nil {
		t.Fatal(err)
	}
	stored, err := hex.DecodeString(keyed.ServerKeys[0].SHA256)
	if err != nil || !credhash.Keyed(stored) || !policy.Matches(api.DataAPIServerKeyCredentialTag, sum[:], stored) {
		t.Fatalf("stored server key %q is not keyed", keyed.ServerKeys[0].SHA256)
	}
	if err := keyed.Validate(); err != nil {
		t.Fatalf("keyed settings no longer validate: %v", err)
	}
	again, err := keyServerKeys(out)
	if err != nil || string(again) != string(out) {
		t.Fatal("a keyed hash was keyed again")
	}
}
