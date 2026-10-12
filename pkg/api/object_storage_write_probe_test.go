// adr: 1027 — reserved write probe requests carry an exact managed permission.
package api

import "testing"

func TestObjectStorageWriteProbeAdmissionShape(t *testing.T) {
	for _, command := range [][]string{
		{AppTaskObjectStorageWriteProbeCommand, "ASSETS", "write"},
		{AppTaskObjectStorageWriteProbeCommand, "ASSETS", "read_write"},
	} {
		request := CreateAppTaskRequest{Command: command, VerificationDeploymentID: "11111111-1111-4111-8111-111111111111"}
		if _, p := request.Resolve(); p != nil || !IsBindingVerificationCommand(command, false) {
			t.Fatalf("valid command rejected: %v", p)
		}
	}
	for _, command := range [][]string{{AppTaskObjectStorageWriteProbeCommand, "ASSETS"}, {AppTaskObjectStorageWriteProbeCommand, "ASSETS", "read"}, {AppTaskObjectStorageWriteProbeCommand, "ASSETS", "write", "key"}, {AppTaskObjectStorageWriteProbeCommand, "bad-prefix", "write"}} {
		if _, p := (CreateAppTaskRequest{Command: command}).Resolve(); p == nil {
			t.Fatalf("invalid command accepted: %v", command)
		}
	}
	if IsBindingVerificationCommand([]string{AppTaskObjectStorageWriteProbeCommand, "ASSETS", "write"}, true) {
		t.Fatal("shell probe admitted")
	}
}
