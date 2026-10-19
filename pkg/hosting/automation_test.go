package hosting

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestHostingCIRunIdentityMustBeCanonical(t *testing.T) {
	// adr: 202610170925 — one textual identity maps to the same key in both stores.
	id := "a1111111-1111-4111-8111-111111111111"
	if !ValidCIRunID(id) {
		t.Fatal("canonical UUID rejected")
	}
	for _, invalid := range []string{strings.ToUpper(id), strings.ReplaceAll(id, "-", ""), "urn:uuid:" + id, "{" + id + "}", "bad"} {
		if ValidCIRunID(invalid) {
			t.Fatal("noncanonical run identity accepted", invalid)
		}
	}
}

func TestCIReservationOptInPreservesLegacyFingerprint(t *testing.T) {
	// adr: 202610171216 — omitted false remains byte-for-byte compatible with old run fingerprints.
	req := api.HostingCIRunRequest{PreviewTTLSeconds: 3600}
	legacy := struct {
		Manifest          api.HostingManifest           `json:"manifest"`
		Provenance        *api.HostingReleaseProvenance `json:"provenance,omitempty"`
		PreviewTTLSeconds int64                         `json:"preview_ttl_seconds"`
	}{req.Manifest, req.Provenance, req.PreviewTTLSeconds}
	before, _ := json.Marshal(legacy)
	after, _ := json.Marshal(req)
	if string(before) != string(after) {
		t.Fatal("legacy fingerprint input changed")
	}
	plain, _ := CIFingerprint(req)
	req.ReservePreview = true
	reserved, _ := CIFingerprint(req)
	if plain == reserved {
		t.Fatal("reservation mode not bound to intent")
	}
}
