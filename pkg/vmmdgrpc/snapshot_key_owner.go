package vmmdgrpc

import (
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// InstanceDeploymentLookup is the live instance's deployment, which
// fcvm.Manager records at boot.
type InstanceDeploymentLookup interface {
	InstanceDeploymentIDAndAppID(instance string) (deploymentID, appID string, err error)
}

// snapshotKeysOwned refuses a snapshot whose storage keys are not under the
// instance's own deployment (bug hunt H95). vmmd published a pause or warm
// capture under whatever keys the caller named, so a caller that could reach
// these RPCs could overwrite another app's snapshot with this instance's
// memory, which the other app would then restore. Every capture schedd makes
// lives under snap/<deploymentID>/ (state.SnapshotCaptureMemKey and the
// legacy storagekey.Snapshot* keys). A legacy wake that carried no
// deployment ID, or an instance that is not live (which the capture itself
// then refuses), is not checked.
func (s *Server) snapshotKeysOwned(instance string, keys ...string) error {
	lookup, ok := s.vmm.(InstanceDeploymentLookup)
	if !ok {
		return nil
	}
	// An instance that is not live has no deployment to check against; the
	// capture itself refuses it, with the error the caller already handles.
	deploymentID := liveInstanceDeployment(lookup, instance)
	if deploymentID == "" {
		return nil
	}
	prefix := "snap/" + deploymentID + "/"
	for _, key := range keys {
		if key == "" {
			continue
		}
		if !strings.HasPrefix(key, prefix) || strings.Contains(key[len(prefix):], "..") {
			return status.Errorf(codes.PermissionDenied, "snapshot key %q is not under instance %s's deployment", key, instance)
		}
	}
	return nil
}

// liveInstanceDeployment is the instance's deployment, or "" when it is not
// live or was woken without one.
func liveInstanceDeployment(lookup InstanceDeploymentLookup, instance string) string {
	deploymentID, _, err := lookup.InstanceDeploymentIDAndAppID(instance)
	if err == nil {
		return deploymentID
	}
	return ""
}
