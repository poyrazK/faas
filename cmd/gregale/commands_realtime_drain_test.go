package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdRealtimeDrainSendsBoundedSelection(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"operation_id":"drain-1","status":"running","results":[{"id":"conn-a","status":"pending"}],"matched":1,"closed":0,"gone":0,"failed":0,"limit":25,"truncated":false,"dry_run":false,"partial":false,"nodes_queried":1,"nodes_unavailable":0}`, http.StatusAccepted)
	oldOut, oldErr := osStdout, osStderr
	var out, stderr bytes.Buffer
	osStdout, osStderr = &out, &stderr
	t.Cleanup(func() {
		osStdout, osStderr = oldOut, oldErr
	})

	if code := cmdRealtimeDrain([]string{"demo", "endpoint-1", "--reason", "deploy", "--channel", "room-a", "--principal", "user-a", "--connection-id", "conn-a", "--connection-id", "conn-b", "--limit", "25"}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	if f.sawMethod != http.MethodPost || f.sawPath != "/v1/apps/demo/realtime/endpoints/endpoint-1/connections/drain" {
		t.Fatalf("route = %s %s", f.sawMethod, f.sawPath)
	}
	var request api.ManagedRealtimeDrainRequest
	if err := json.Unmarshal(f.sawBody, &request); err != nil {
		t.Fatal(err)
	}
	if request.Reason != "deploy" || request.Channel != "room-a" || request.Principal != "user-a" || request.Limit != 25 || len(request.ConnectionIDs) != 2 || request.ConnectionIDs[1] != "conn-b" {
		t.Fatalf("request = %+v", request)
	}
	if !bytes.Contains(out.Bytes(), []byte("Realtime drain accepted; operation drain-1 is running.")) || stderr.Len() != 0 {
		t.Fatalf("output = %q stderr = %q", out.String(), stderr.String())
	}
}
