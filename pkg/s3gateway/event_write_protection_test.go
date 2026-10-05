package s3gateway

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// adr: 599
func TestEventWriteProtectionHeaders(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers map[string]string
		valid   bool
	}{
		{"on", map[string]string{"Mode": "COMPLIANCE", "Event-Hold": "ON", "Event-Hold-Duration-Days": "30"}, true},
		{"on-years", map[string]string{"Mode": "GOVERNANCE", "Event-Hold": "ON", "Event-Hold-Duration-Years": "1"}, true},
		{"off-fixed", map[string]string{"Mode": "COMPLIANCE", "Event-Hold": "OFF", "Retain-Until-Date": "2027-01-01T00:00:00Z"}, true},
		{"off-without-date", map[string]string{"Mode": "COMPLIANCE", "Event-Hold": "OFF"}, false},
		{"missing-duration", map[string]string{"Mode": "COMPLIANCE", "Event-Hold": "ON"}, false},
		{"two-equal-durations", map[string]string{"Mode": "COMPLIANCE", "Event-Hold": "ON", "Event-Hold-Duration-Days": "1", "Event-Hold-Duration-Years": "1"}, false},
		{"zero-duration", map[string]string{"Mode": "COMPLIANCE", "Event-Hold": "ON", "Event-Hold-Duration-Days": "0"}, false},
		{"overflow-duration", map[string]string{"Mode": "COMPLIANCE", "Event-Hold": "ON", "Event-Hold-Duration-Days": "2147483648"}, false},
		{"duration-without-event", map[string]string{"Mode": "COMPLIANCE", "Retain-Until-Date": "2027-01-01T00:00:00Z", "Event-Hold-Duration-Days": "30"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("PUT", "https://s3.example.test/assets/key", nil)
			signed := []string{}
			for name, value := range tc.headers {
				full := "X-Amz-Object-Lock-" + name
				r.Header.Set(full, value)
				signed = append(signed, strings.ToLower(full))
			}
			p, err := writeProtectionFromHeaders(r, strings.Join(signed, ";"))
			if (err == nil) != tc.valid {
				t.Fatal(p, err)
			}
			if tc.valid {
				if _, err = writeProtectionFromHeaders(r, "host"); err == nil {
					t.Fatal("unsigned selection accepted")
				}
				r.Header.Add("X-Amz-Object-Lock-Event-Hold", r.Header.Get("X-Amz-Object-Lock-Event-Hold"))
				if _, err = writeProtectionFromHeaders(r, strings.Join(signed, ";")); err == nil {
					t.Fatal("duplicate event accepted")
				}
			}
		})
	}
}
