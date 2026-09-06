package objectstorage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestObjectStorageUsageReportImport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	fingerprint := strings.Repeat("a", 64)
	r := &Registry{backends: map[string]Backend{"managed": {ID: "managed", Fingerprint: fingerprint}}, usageReportPaths: map[string]string{"managed": path}}
	report := api.ObjectStorageUsageReport{AccountID: "2fffa69f-b206-47db-8d82-8d8d394a630d", BackendID: "managed", BackendFingerprint: fingerprint, Source: "provider", PeriodStart: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), ObservedAt: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)}
	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{"valid", marshalUsageReports(t, report), 1},
		{"missing measurement", `[{"backend_id":"managed"}]`, 0},
		{"partial file", `[{`, 0},
		{"trailing data", marshalUsageReports(t, report) + ` {}`, 0},
		{"wrong placement", strings.ReplaceAll(marshalUsageReports(t, report), fingerprint, strings.Repeat("b", 64)), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			err := r.ImportUsageReports(context.Background(), func(got api.ObjectStorageUsageReport) error {
				calls++
				if got != report {
					t.Fatal(got)
				}
				return nil
			})
			if calls != tc.want || (err == nil) != (tc.want == 1) {
				t.Fatal(calls, err)
			}
		})
	}
}

func marshalUsageReports(t *testing.T, r api.ObjectStorageUsageReport) string {
	t.Helper()
	data, err := json.Marshal([]api.ObjectStorageUsageReport{r})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
