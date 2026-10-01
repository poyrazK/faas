// adr: 375
package vmmdgrpc

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/onebox-faas/faas/pkg/fcvm"
)

// This helper lets configured gateway tests use the production vmmd owner
// without importing VM lifecycle code into a gateway daemon. Only VM startup,
// namespace selection and the guest HTTP server are fixtures.
func TestNodeAdmissionVMMDProcess(t *testing.T) {
	path := os.Getenv("GREGALE_NODE_ADMISSION_VMMD_SPEC")
	if path == "" {
		t.Skip("subprocess helper")
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var instances []nodeAdmissionFixtureInstance
	if err := json.Unmarshal(encoded, &instances); err != nil || len(instances) == 0 {
		t.Fatalf("instance fixture: %v", err)
	}
	f := newNodeAdmissionProcessFixtureWith(t, instances, true)
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		states := make(map[string]fcvm.HTTPAdmissionStatus, len(instances))
		for _, spec := range instances {
			states[spec.ID], _ = f.owner.HTTPAdmissionStatus(spec.ID)
		}
		_ = json.NewEncoder(w).Encode(struct {
			Instances              map[string]fcvm.HTTPAdmissionStatus
			RPCs, GuestCalls, Peak int32
		}{states, f.rpcCalls.Load(), f.guestCalls.Load(), f.peak.Load()})
	}))
	defer control.Close()
	ready := struct {
		Target, Control string
		Port            int
	}{"unix://" + f.listener.Addr().String(), control.URL, f.port}
	if err := json.NewEncoder(os.Stdout).Encode(ready); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}
