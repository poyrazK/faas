// adr: 590
package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestProjectsEnvironmentQueuesUsesScopedEndpoint(t *testing.T) {
	for _, verb := range []string{"get", "set"} {
		t.Run(verb, func(t *testing.T) {
			main.ResetJSONOut(t)
			fake := main.AuthedFakeAPI(t, `{"environment":"stage","workload":"shop-worker","revision":1,"workload_revision":4,"config_hash":"hash","activation_state":"unavailable","bindings":[]}`, http.StatusOK)
			oldIn, oldOut := (*main.OsStdin), (*main.OsStdout)
			(*main.OsStdin) = strings.NewReader(`{"expected_revision":3,"bindings":[]}`)
			var output bytes.Buffer
			(*main.OsStdout) = &output
			t.Cleanup(func() { (*main.OsStdin), (*main.OsStdout) = oldIn, oldOut })
			args := []string{"queues", verb, "shop", "stage", "shop-worker"}
			method := http.MethodGet
			if verb == "set" {
				args = append(args, "--stdin")
				method = http.MethodPut
			}
			if exit := main.CmdProjectsEnvironments(args); exit != 0 {
				t.Fatalf("exit=%d", exit)
			}
			if (*fake.SawMethod()) != method || (*fake.SawPath()) != "/v1/projects/shop/environments/stage/workloads/shop-worker/queue-bindings" {
				t.Fatalf("request=%s %s", (*fake.SawMethod()), (*fake.SawPath()))
			}
			if verb == "set" {
				var request api.ReplaceProjectEnvironmentQueueBindingsRequest
				if err := json.Unmarshal((*fake.SawBody()), &request); err != nil || request.ExpectedRevision == nil || *request.ExpectedRevision != 3 || request.Bindings == nil || len(*request.Bindings) != 0 {
					t.Fatalf("missing complete collection/revision: %s, %v", (*fake.SawBody()), err)
				}
				if !strings.Contains(output.String(), "workload revision 4") || !strings.Contains(output.String(), "consumer activation: unavailable") {
					t.Fatalf("output=%s", output.String())
				}
			} else {
				var response api.ProjectEnvironmentQueueBindingsResponse
				if err := json.Unmarshal(output.Bytes(), &response); err != nil || response.WorkloadRevision != 4 {
					t.Fatalf("read omitted usable queue configuration: %s, %v", output.String(), err)
				}
			}
		})
	}
}

func TestProjectsEnvironmentQueuesRejectsIncompleteInputBeforeRequest(t *testing.T) {
	for _, body := range []string{`{}`, `{"expected_revision":0}`, `{"bindings":[]}`, `{"expected_revision":-1,"bindings":[]}`, `{"expected_revision":0,"bindings":null}`, `{"expected_revision":0,"bindings":[],"source_id":"prod"}`, `{"expected_revision":0,"bindings":[]} {}`} {
		t.Run(body, func(t *testing.T) {
			main.ResetJSONOut(t)
			fake := main.AuthedFakeAPI(t, "{}", http.StatusOK)
			oldIn := (*main.OsStdin)
			(*main.OsStdin) = strings.NewReader(body)
			t.Cleanup(func() { (*main.OsStdin) = oldIn })
			if exit := main.CmdProjectsEnvironmentQueues([]string{"set", "shop", "stage", "worker", "--stdin"}); exit != 1 || (*fake.SawMethod()) != "" {
				t.Fatalf("incomplete config reached API: exit=%d request=%s", exit, (*fake.SawMethod()))
			}
		})
	}
}

func TestProjectsEnvironmentQueuesFileFlagKeepsItsValueAndScopedPositionals(t *testing.T) {
	for _, layout := range []string{"before", "after", "interspersed", "equals"} {
		t.Run(layout, func(t *testing.T) {
			main.ResetJSONOut(t)
			fake := main.AuthedFakeAPI(t, `{"environment":"stage","workload":"worker","workload_revision":4,"activation_state":"unavailable","bindings":[]}`, http.StatusOK)
			path := filepath.Join(t.TempDir(), "stage queues.json")
			if err := os.WriteFile(path, []byte(`{"expected_revision":3,"bindings":[]}`), 0o600); err != nil {
				t.Fatal(err)
			}
			oldOut := (*main.OsStdout)
			var output bytes.Buffer
			(*main.OsStdout) = &output
			t.Cleanup(func() { (*main.OsStdout) = oldOut })
			var args []string
			switch layout {
			case "before":
				args = []string{"queues", "set", "--file", path, "shop", "stage", "worker"}
			case "after":
				args = []string{"queues", "set", "shop", "stage", "worker", "--file", path}
			case "interspersed":
				args = []string{"queues", "set", "shop", "--file", path, "stage", "worker"}
			case "equals":
				args = []string{"queues", "set", "--file=" + path, "shop", "stage", "worker"}
			}
			if exit := main.CmdProjectsEnvironments(args); exit != 0 {
				t.Fatalf("exit=%d", exit)
			}
			if (*fake.SawMethod()) != http.MethodPut || (*fake.SawPath()) != "/v1/projects/shop/environments/stage/workloads/worker/queue-bindings" {
				t.Fatalf("request=%s %s", (*fake.SawMethod()), (*fake.SawPath()))
			}
			var request api.ReplaceProjectEnvironmentQueueBindingsRequest
			if err := json.Unmarshal((*fake.SawBody()), &request); err != nil || request.ExpectedRevision == nil || *request.ExpectedRevision != 3 || request.Bindings == nil || len(*request.Bindings) != 0 {
				t.Fatalf("missing complete collection/revision: %s, %v", (*fake.SawBody()), err)
			}
		})
	}
}
