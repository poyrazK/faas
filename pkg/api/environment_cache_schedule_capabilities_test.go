package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestScheduleCapabilitiesMixedVersions(t *testing.T) {
	for _, tc := range []struct {
		body string
		want bool
	}{
		{body: `{"plan":"free","capabilities":[]}`},
		{body: `{"environment_cache_schedules":{"listing":true,"activity":true,"checkpoint":false,"retention_seconds":2592000}}`},
		{body: `{"environment_cache_schedules":{"listing":true,"activity":true,"checkpoint":true,"retention_seconds":1}}`},
		{body: `{"environment_cache_schedules":{"listing":true,"activity":true,"checkpoint":true,"retention_seconds":2592000}}`, want: true},
	} {
		t.Run(tc.body, func(t *testing.T) {
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/capabilities" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer b.Close()
			got, err := NewClient(b.URL, "fixture-token").GetCapabilities(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got.EnvironmentCacheSchedules.SupportsRecovery() != tc.want {
				t.Fatalf("unsafe support result: %+v", got)
			}
		})
	}
}
