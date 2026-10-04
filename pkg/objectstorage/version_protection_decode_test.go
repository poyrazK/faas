package objectstorage

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 572
func TestVersionProtectionStrictInput(t *testing.T) {
	for _, body := range []string{`{"id":"id"}`, `{"id":"id","retention":null}`, `{"id":"id","retention":{},"retention":{}}`, `{"id":"id","retention":{"Mode":"COMPLIANCE"}}`, `{"id":"id","retention":{},"bypass":true}`, `{"id":"id","retention":{}} {}`, strings.Repeat(" ", int(api.MaxObjectLockBodyBytes)+1)} {
		if _, _, err := DecodeObjectVersionProtectionRequest([]byte(body), "retention"); err == nil {
			t.Fatal("unsafe JSON accepted", body[:min(len(body), 120)])
		}
	}
	for _, body := range []string{`<LegalHold/>`, `<LegalHold><Status>ON</Status><Status>OFF</Status></LegalHold>`, `<LegalHold><Status>ON</Status><Bypass>true</Bypass></LegalHold>`, `<LegalHold><Status>ON</Status></LegalHold><LegalHold/>`} {
		if _, err := DecodeObjectVersionLegalHold([]byte(body)); err == nil {
			t.Fatal("unsafe XML accepted", body)
		}
	}
	if r, _, err := DecodeObjectVersionProtectionRequest([]byte(`{"id":"id","retention":{}}`), "retention"); err != nil || !r.Retention.Empty() {
		t.Fatal("explicit clear", r, err)
	}
	now := time.Now().UTC()
	until := now.Add(time.Hour).Truncate(time.Millisecond).Add(time.Nanosecond)
	r := api.ObjectVersionRetention{Mode: "COMPLIANCE", RetainUntilDate: &until}
	snapshot := r.ForWrite()
	if snapshot.RetainUntilDate.Before(until) || snapshot.RetainUntilDate.Nanosecond()%int(time.Millisecond) != 0 {
		t.Fatal("snapshot shortened retention", snapshot)
	}
	for _, next := range []api.ObjectVersionRetention{{}, {Mode: "GOVERNANCE", RetainUntilDate: &until}, {Mode: "COMPLIANCE", RetainUntilDate: &now}} {
		if err := validateRetentionChange(r, next, now); err == nil {
			t.Fatal("active protection weakened", next)
		}
	}
	extended := until.Add(time.Hour)
	if err := validateRetentionChange(r, api.ObjectVersionRetention{Mode: "COMPLIANCE", RetainUntilDate: &extended}, now); err != nil {
		t.Fatal(err)
	}
}
