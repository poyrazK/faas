package workflowstart

import (
	"bytes"
	"encoding/json"
	"testing"
)

// adr: 20261104120000 — an Idempotency-Key never replays a run of another
// environment: a stage's fingerprint differs from production's, which keeps
// the fingerprint earlier releases stored.
func TestScopedFingerprint(t *testing.T) {
	input := json.RawMessage(`{"a":1}`)
	production := Fingerprint(input)
	for _, scope := range []string{"default", "production"} {
		if !bytes.Equal(scopedFingerprint(input, scope), production) {
			t.Fatalf("scope %q changed production's fingerprint", scope)
		}
	}
	staging, preview := scopedFingerprint(input, "staging"), scopedFingerprint(input, "preview")
	if bytes.Equal(staging, production) || bytes.Equal(staging, preview) {
		t.Fatal("stage fingerprints must differ from production's and each other's")
	}
}
