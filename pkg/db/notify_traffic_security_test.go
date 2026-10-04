// adr: 570
package db

import "testing"

func TestTrafficSecurityNotificationRejectsInvalidIdentityAndGeneration(t *testing.T) {
	for _, raw := range []string{
		`{}`, `{"scope_kind":"account","scope_id":"00000000-0000-0000-0000-000000000000","revision":1}`,
		`{"scope_kind":"policy","scope_id":"12345678-1234-1234-1234-123456789abc","revision":1}`,
		`{"scope_kind":"app","scope_id":"12345678-1234-1234-1234-123456789abc","revision":0}`,
		`{"scope_kind":"app","scope_id":"bad","revision":1}`, `[]`,
	} {
		if _, err := ParseTrafficSecurityChangedPayload(raw); err == nil {
			t.Fatalf("invalid notification accepted: %s", raw)
		}
	}
	for _, kind := range []string{"account", "app", "deployment"} {
		payload, err := ParseTrafficSecurityChangedPayload(`{"scope_kind":"` + kind + `","scope_id":"12345678-1234-1234-1234-123456789ABC","revision":2}`)
		if err != nil || payload.ScopeID != "12345678-1234-1234-1234-123456789abc" {
			t.Fatalf("valid notification=%v/%v", payload, err)
		}
	}
}
