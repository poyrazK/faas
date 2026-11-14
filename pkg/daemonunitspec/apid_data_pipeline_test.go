package daemonunitspec

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPipelineWorkerUnitsAreOptionalAndGenerated(t *testing.T) {
	for _, tc := range []struct {
		name, flag  string
		credentials int
	}{
		{"apid-data-pipeline-worker", "--data-pipeline-worker", 4},
		{"apid-data-pipeline-alert-worker", "--data-pipeline-alert-worker", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var found *Entry
			for i := range OptionalRegistry {
				if OptionalRegistry[i].Name == tc.name {
					found = &OptionalRegistry[i]
					break
				}
			}
			if found == nil {
				t.Fatal("worker is not optional")
			}
			u := found.Unit()
			if u.Type != "simple" || u.NotifyAccess != "" || u.WatchdogSec != "" || u.Restart != "on-failure" {
				t.Fatalf("worker startup/recovery settings: type=%q notify=%q watchdog=%q restart=%q", u.Type, u.NotifyAccess, u.WatchdogSec, u.Restart)
			}
			if !slices.Contains(u.Wants, "postgresql.service") || slices.Contains(u.Requires, "postgresql.service") {
				t.Fatalf("database restarts must not stop the worker: Wants=%v Requires=%v", u.Wants, u.Requires)
			}
			if !strings.Contains(u.ExecStart, tc.flag) || len(u.LoadCredential) != tc.credentials {
				t.Fatalf("unexpected worker command or credentials: %q, %v", u.ExecStart, u.LoadCredential)
			}
			if tc.credentials == 0 && len(u.ReadWritePaths) != 0 {
				t.Fatalf("alert worker has writable paths: %v", u.ReadWritePaths)
			}
			for _, dir := range []string{"deploy/systemd", "deploy/ansible/roles/control_plane_service/files"} {
				path := filepath.Join("..", "..", dir, "faas-"+tc.name+".service")
				got, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, u.Render()) {
					t.Errorf("%s differs from generated unit", path)
				}
			}
		})
	}
}
