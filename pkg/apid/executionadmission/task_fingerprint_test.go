package executionadmission_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/executionadmission"
	"testing"
)

func TestTaskToolFingerprintBindsProviderAndCanonicalInput(t *testing.T) {
	a := api.InvokeExecutionTaskRequest{Name: "report", Version: 1, Input: json.RawMessage(`{"a":1,"b":2}`)}
	b := a
	b.Input = json.RawMessage(`{"b":2,"a":1}`)
	key := []byte("private-host-key")
	first, err := executionadmission.TaskToolFingerprint(a, "provider-a", key)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := executionadmission.TaskToolFingerprint(b, "provider-a", key)
	if err != nil || first != replay {
		t.Fatal("canonical replay changed", err)
	}
	changed, err := executionadmission.TaskToolFingerprint(a, "provider-b", key)
	if err != nil || changed == first {
		t.Fatal("provider identity is unbound", err)
	}
	if first[0] != changed[0] || first[1] != changed[1] || first[2] != changed[2] || first[3] != changed[3] {
		t.Fatal("provider identity changed key epoch")
	}
}
