// adr: 050
package githubd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPushNonFastForwardComparisonRebuildsEveryApp(t *testing.T) {
	for _, tc := range []struct {
		name, response string
	}{
		{"diverged", `{"status":"diverged","ahead_by":1,"behind_by":1,"total_commits":1,"commits":[{"sha":"head456"}],"files":[{"filename":"services/auth/api/index.ts","status":"modified"}]}`},
		{"behind", `{"status":"behind","ahead_by":0,"behind_by":1,"total_commits":0,"commits":[],"files":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, tc.response)
			}))
			t.Cleanup(server.Close)
			client, _ := fixedClient(t, server, "installation-token")
			svc, enqueuer, _ := pathFilterRig(t, client, true)
			// GitHub compares the merge base to the new head. Its files list
			// omits edits removed from billing when this branch was replaced.
			body := []byte(`{"ref":"refs/heads/main","before":"base123","after":"head456","forced":true,"repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
			if _, err := svc.HandlePushRequest(t.Context(), body); err != nil {
				t.Fatal(err)
			}
			if len(enqueuer.calls) != 3 {
				t.Fatalf("builds queued = %d, want all 3 apps after a non-fast-forward comparison", len(enqueuer.calls))
			}
		})
	}
}

func TestPushSharedFileAndMemberChangeRebuildsEveryApp(t *testing.T) {
	for _, shared := range []string{"package-lock.json", ".github/workflows/build.yml"} {
		t.Run(shared, func(t *testing.T) {
			changes := &stubChangedFiles{files: []string{shared, "services/auth/api/index.ts"}}
			svc, enqueuer, _ := pathFilterRig(t, changes, false)
			body := []byte(`{"ref":"refs/heads/main","before":"base123","after":"head456","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
			if _, err := svc.HandlePushRequest(t.Context(), body); err != nil {
				t.Fatal(err)
			}
			if len(enqueuer.calls) != 2 {
				t.Fatalf("builds queued = %d, want both apps after the shared file changed", len(enqueuer.calls))
			}
		})
	}
}
