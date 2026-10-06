package state_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 622
func TestObjectEventWriteProtectionMinimum(t *testing.T) {
	now := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	day, year, fixed := int32(30), int32(1), int32(400)
	explicit := now.AddDate(2, 0, 0)
	base := state.ObjectWriteProtectionSnapshot{Enabled: true, CapturedAt: &now, DefaultRetention: &api.ObjectLockDefaultRetention{Mode: "COMPLIANCE", Days: &fixed, DefaultEventHold: &api.ObjectRetentionPeriod{Years: &year}}}
	for _, tc := range []struct {
		name      string
		requested *api.ObjectVersionRetention
		want      time.Time
		event     string
	}{
		{"default preserves longer fixed minimum", nil, now.AddDate(0, 0, 400), "ON"},
		{"explicit event replaces default", &api.ObjectVersionRetention{Mode: "GOVERNANCE", EventHold: "ON", EventHoldDuration: &api.ObjectRetentionPeriod{Days: &day}}, now.AddDate(0, 0, 30), "ON"},
		{"explicit event minimum", &api.ObjectVersionRetention{Mode: "COMPLIANCE", EventHold: "ON", EventHoldDuration: &api.ObjectRetentionPeriod{Days: &day}, RetainUntilDate: &explicit}, explicit, "ON"},
		{"explicit fixed overrides event default", &api.ObjectVersionRetention{Mode: "GOVERNANCE", RetainUntilDate: &explicit}, explicit, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := base.Clone()
			p.Requested.Retention = tc.requested
			r := p.MinimumRetention()
			if !p.Valid() || r.EventHold != tc.event || !r.RetainUntilDate.Equal(tc.want) {
				t.Fatal(p, r)
			}
			var recovered state.ObjectWriteProtectionSnapshot
			data, _ := json.Marshal(p)
			if err := json.Unmarshal(data, &recovered); err != nil || !recovered.Equal(p) || recovered.Proof() != p.Proof() {
				t.Fatal("snapshot changed after reconstruction", err)
			}
		})
	}
	end := time.Date(9999, 12, 1, 0, 0, 0, 0, time.UTC)
	base.CapturedAt = &end
	if base.Valid() {
		t.Fatal("duration overflow accepted")
	}
}
