// adr: 20261019120007
package apidserver_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

// The dry run shows the release command the apply runs, including one a
// service declares in its own directory (agent eval 2026-11-24, voting app:
// worker/Procfile release: ran, but the plan never said so).
func TestPlanWorkloadReleaseCommand(t *testing.T) {
	composeWorker := reposcan.Workload{Name: "worker", RootDir: "worker", Class: reposcan.ClassWorker, DetectedBy: reposcan.Detection{Detector: reposcan.DetectorCompose}}
	composeVote := reposcan.Workload{Name: "vote", RootDir: "vote", Class: reposcan.ClassHTTP, DetectedBy: reposcan.Detection{Detector: reposcan.DetectorCompose}}
	folded := composeVote
	folded.ReleaseCommand, folded.ReleaseCommandShell, folded.ReleaseService = []string{"npm run migrate"}, true, "migrate"
	for _, tc := range []struct {
		name                  string
		files                 map[string]string
		workloads             []reposcan.Workload
		workload              reposcan.Workload
		phaseOff              bool
		want, wantSrc, wantIn string
	}{
		{
			name:      "compose build context procfile",
			files:     map[string]string{"worker/Dockerfile": "FROM scratch\n", "worker/Procfile": "release: dotnet Worker.dll migrate\nworker: dotnet Worker.dll\n", "vote/Dockerfile": "FROM scratch\n"},
			workloads: []reposcan.Workload{composeVote, composeWorker},
			workload:  composeWorker,
			want:      "dotnet Worker.dll migrate", wantSrc: "worker/Procfile release:",
		},
		{
			name:      "service gregale.yaml",
			files:     map[string]string{"worker/Dockerfile": "FROM scratch\n", "worker/gregale.yaml": "release:\n  command: ./migrate\n", "vote/Dockerfile": "FROM scratch\n"},
			workloads: []reposcan.Workload{composeVote, composeWorker},
			workload:  composeWorker,
			want:      "./migrate", wantSrc: "worker/gregale.yaml release.command",
		},
		{
			name:      "compose completion step",
			files:     map[string]string{"vote/Dockerfile": "FROM scratch\n", "worker/Dockerfile": "FROM scratch\n"},
			workloads: []reposcan.Workload{folded, composeWorker},
			workload:  folded,
			want:      "npm run migrate", wantSrc: "Compose service migrate (service_completed_successfully)",
		},
		{
			name:      "shared directory is refused",
			files:     map[string]string{"svc/Procfile": "release: ./migrate\n"},
			workloads: []reposcan.Workload{{Name: "a", RootDir: "svc"}, {Name: "b", RootDir: "svc"}},
			workload:  reposcan.Workload{Name: "a", RootDir: "svc"},
			wantIn:    "both build from that directory",
		},
		{
			name:      "release phase off shows the scan only",
			files:     map[string]string{"worker/Dockerfile": "FROM scratch\n", "worker/Procfile": "release: ./migrate\n", "vote/Dockerfile": "FROM scratch\n"},
			workloads: []reposcan.Workload{composeVote, composeWorker},
			workload:  composeWorker,
			phaseOff:  true,
		},
		{
			name:      "release phase off warns about a service's own command",
			files:     map[string]string{"worker/gregale.yaml": "release:\n  command: ./migrate\n"},
			workloads: []reposcan.Workload{{Name: "vote", RootDir: "vote"}, {Name: "worker", RootDir: "worker"}},
			workload:  reposcan.Workload{Name: "worker", RootDir: "worker"},
			phaseOff:  true,
			want:      "./migrate", wantSrc: "worker/gregale.yaml release.command", wantIn: "not available on this Gregale installation",
		},
		{
			name:      "manifest label names the file read",
			files:     map[string]string{"gregale.yaml": "release:\n  command: ./migrate\n"},
			workloads: []reposcan.Workload{{Name: "vote", RootDir: "vote"}, {Name: "worker", RootDir: "worker"}},
			workload:  reposcan.Workload{Name: "worker", RootDir: "worker"},
			want:      "./migrate", wantSrc: "gregale.yaml release.command",
		},
		{
			name:      "single workload root procfile",
			files:     map[string]string{"Procfile": "release: bin/migrate\nweb: bin/web\n"},
			workloads: []reposcan.Workload{{Name: "web"}},
			workload:  reposcan.Workload{Name: "web"},
			want:      "bin/migrate", wantSrc: "Procfile release:",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, body := range tc.files {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got, src, note := apidserver.PlanWorkloadReleaseCommand(dir, tc.workloads, tc.workload, tc.phaseOff)
			if got != tc.want || src != tc.wantSrc {
				t.Fatalf("release = %q (%q), want %q (%q); note %q", got, src, tc.want, tc.wantSrc, note)
			}
			if (tc.wantIn == "") != (note == "") || !strings.Contains(note, tc.wantIn) {
				t.Fatalf("note = %q, want containing %q", note, tc.wantIn)
			}
		})
	}
}
