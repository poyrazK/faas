package object

import (
	"testing"
	"time"
)

func TestProviderHealthRefusalLapses(t *testing.T) {
	at := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	backends := []string{"gcs-a", "gcs-b"}
	for _, tc := range []struct {
		name    string
		mark    string
		success bool
		after   time.Duration
		want    bool
	}{
		{name: "no refusal", after: time.Minute},
		{name: "fresh refusal withdraws", mark: "gcs-b", after: time.Minute, want: true},
		{name: "refusal holds until the hold ends", mark: "gcs-a", after: providerRefusalHold - time.Second, want: true},
		{name: "refusal lapses after the hold", mark: "gcs-a", after: providerRefusalHold},
		{name: "success clears", mark: "gcs-a", success: true, after: time.Minute},
		{name: "other backends do not count", mark: "gcs-c", after: time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var h providerHealth
			h.markConfigurationFailure(tc.mark, at)
			if tc.success {
				h.markSuccess(tc.mark)
			}
			if got := h.anyFailing(backends, at.Add(tc.after)); got != tc.want {
				t.Fatalf("anyFailing = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestProviderHealthLapsedRefusalStaysClearedUntilTheNextOne(t *testing.T) {
	at := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	var h providerHealth
	h.markConfigurationFailure("gcs-a", at)
	if h.anyFailing([]string{"gcs-a"}, at.Add(providerRefusalHold)) {
		t.Fatal("refusal should lapse after the hold")
	}
	// A refusal far in the past must not come back once the clock moves.
	if h.anyFailing([]string{"gcs-a"}, at.Add(providerRefusalHold/2)) {
		t.Fatal("a lapsed refusal came back")
	}
	renewed := at.Add(2 * providerRefusalHold)
	h.markConfigurationFailure("gcs-a", renewed)
	if !h.anyFailing([]string{"gcs-a"}, renewed.Add(time.Minute)) {
		t.Fatal("a new refusal should withdraw the backend again")
	}
}
