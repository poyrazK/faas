package executionadmission_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/executionadmission"
)

func TestFingerprintCoversCanonicalRequestAndLargePayloads(t *testing.T) {
	request := api.CreateExecutionRequest{
		Runtime: api.ExecutionRuntimeNode22, Source: strings.Repeat(" ", 700<<10) + "export default () => 1",
		Input: json.RawMessage(`{"b":2,"a":"` + strings.Repeat("x", 400<<10) + `"}`),
	}
	key := []byte("stable-host-key")
	first, err := executionadmission.Fingerprint(request, key)
	if err != nil {
		t.Fatal(err)
	}
	request.Input = json.RawMessage(`{ "a":"` + strings.Repeat("x", 400<<10) + `", "b": 2 }`)
	same, err := executionadmission.Fingerprint(request, key)
	if err != nil || same != first {
		t.Fatalf("equivalent JSON changed fingerprint: %v", err)
	}
	request.WorkflowID = "different-work"
	changed, err := executionadmission.Fingerprint(request, key)
	if err != nil || changed == first || string(changed[:4]) != string(first[:4]) {
		t.Fatalf("workflow not bound: %v", err)
	}
	rotated, err := executionadmission.Fingerprint(request, []byte("rotated-host-key"))
	if err != nil || string(rotated[:4]) == string(first[:4]) {
		t.Fatalf("rotation not identifiable: %v", err)
	}
	if _, err := executionadmission.Fingerprint(request, nil); err == nil {
		t.Fatal("missing MAC key accepted")
	}
}

func TestAdmissionKeyValidation(t *testing.T) {
	for _, key := range []string{"", "has space", "tab\t", "unicode-☃", strings.Repeat("x", 256)} {
		if executionadmission.ValidKey(key) {
			t.Errorf("accepted %q", key)
		}
	}
	if !executionadmission.ValidKey("run:customer-42/step_1") || !executionadmission.ValidKey(strings.Repeat("x", 255)) {
		t.Fatal("valid key rejected")
	}
}
