package db

import (
	"encoding/json"
	"testing"
)

func TestParseAppChangedPayloadCanonicalAndLegacy(t *testing.T) {
	const appID = "51f496b9-1c33-4756-8f7d-4153149cbba5"

	canonical, err := ParseAppChangedPayload(`{"kind":"restart","app_id":"` + appID + `","wake_id":"wake-1","lifecycle_changed":true}`)
	if err != nil {
		t.Fatalf("canonical parse: %v", err)
	}
	if canonical.AppID != appID || canonical.Kind != "restart" || canonical.WakeID != "wake-1" || !canonical.LifecycleChanged || canonical.Legacy {
		t.Fatalf("canonical payload = %+v", canonical)
	}

	legacy, err := ParseAppChangedPayload("  " + appID + "\n")
	if err != nil {
		t.Fatalf("legacy parse: %v", err)
	}
	if legacy.AppID != appID || legacy.Kind != "updated" || !legacy.Legacy {
		t.Fatalf("legacy payload = %+v", legacy)
	}

	wire, err := MarshalAppChangedPayload(legacy)
	if err != nil {
		t.Fatalf("marshal legacy: %v", err)
	}
	var normalized map[string]any
	if err := json.Unmarshal(wire, &normalized); err != nil {
		t.Fatalf("normalized payload is not JSON: %v", err)
	}
	if normalized["app_id"] != appID || normalized["kind"] != "updated" {
		t.Fatalf("normalized payload = %s", wire)
	}
}

func TestParseAppChangedPayloadRejectsAmbiguousInput(t *testing.T) {
	for _, raw := range []string{
		"",
		"not-json",
		`{}`,
		`{"kind":"updated"}`,
		`{"app_id":"51f496b9-1c33-4756-8f7d-4153149cbba5"}`,
		`{"app_id":42}`,
		`{"app_id":`,
		`null`,
		`[]`,
		`"51f496b9-1c33-4756-8f7d-4153149cbba5"`,
	} {
		t.Run(raw, func(t *testing.T) {
			if payload, err := ParseAppChangedPayload(raw); err == nil {
				t.Fatalf("ParseAppChangedPayload(%q) = %+v, want error", raw, payload)
			}
		})
	}
}
