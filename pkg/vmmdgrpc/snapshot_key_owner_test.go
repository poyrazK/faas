package vmmdgrpc

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type deploymentLookupVMM struct {
	VmmdAPI
	deployments map[string]string
}

func (v deploymentLookupVMM) InstanceDeploymentIDAndAppID(instance string) (string, string, error) {
	dep, ok := v.deployments[instance]
	if !ok {
		return "", "", errors.New("not live")
	}
	return dep, "app", nil
}

// A capture is published only under the instance's own deployment, so no
// caller can overwrite another app's snapshot with this VM's memory (bug hunt
// H95).
func TestSnapshotKeysMustBelongToTheInstance(t *testing.T) {
	s := &Server{vmm: deploymentLookupVMM{deployments: map[string]string{"inst": "dep-a", "legacy": ""}}}
	for _, tc := range []struct {
		name     string
		instance string
		keys     []string
		want     codes.Code
	}{
		{"own capture", "inst", []string{"snap/dep-a/captures/c1/v2/mem", "snap/dep-a/captures/c1/v2/vmstate"}, codes.OK},
		{"own warm capture", "inst", []string{"snap/dep-a/warm/captures/c2/v2/mem"}, codes.OK},
		{"legacy key", "inst", []string{"snap/dep-a/mem", "snap/dep-a/vmstate"}, codes.OK},
		{"empty vmstate key", "inst", []string{"snap/dep-a/mem", ""}, codes.OK},
		{"another deployment", "inst", []string{"snap/dep-b/captures/c1/v2/mem"}, codes.PermissionDenied},
		{"prefix of another deployment", "inst", []string{"snap/dep-ab/mem"}, codes.PermissionDenied},
		{"vmstate elsewhere", "inst", []string{"snap/dep-a/mem", "snap/dep-b/vmstate"}, codes.PermissionDenied},
		{"traversal", "inst", []string{"snap/dep-a/../dep-b/mem"}, codes.PermissionDenied},
		{"outside snap/", "inst", []string{"layers/dep-a.ext4"}, codes.PermissionDenied},
		{"legacy wake without deployment", "legacy", []string{"snap/anything/mem"}, codes.OK},
		{"not live", "gone", []string{"snap/dep-b/mem"}, codes.OK},
	} {
		if got := status.Code(s.snapshotKeysOwned(tc.instance, tc.keys...)); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}
