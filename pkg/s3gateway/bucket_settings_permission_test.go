package s3gateway

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// Bucket-wide settings and object retention take a customer-created
// read_write key. Write-only uploader keys and compute-binding keys delivered
// into an app's VM keep their object access but not the settings.
func TestBucketSettingsPermission(t *testing.T) {
	h := &Handler{}
	for _, tc := range []struct {
		name       string
		credential state.ObjectS3Credential
		settings   bool
		write      bool
	}{
		{"customer read_write key", state.ObjectS3Credential{Permission: state.ObjectBucketPermissionReadWrite}, true, true},
		{"customer write-only key", state.ObjectS3Credential{Permission: state.ObjectBucketPermissionWrite}, false, true},
		{"customer read-only key", state.ObjectS3Credential{Permission: state.ObjectBucketPermissionRead}, false, false},
		{"compute binding key", state.ObjectS3Credential{Permission: state.ObjectBucketPermissionReadWrite, ManagedAppID: "app_1"}, false, true},
		{"prefix-scoped compute binding key", state.ObjectS3Credential{Permission: state.ObjectBucketPermissionReadWrite, ManagedAppID: "app_1", ManagedPrefix: "uploads/"}, false, true},
	} {
		if got := h.can(tc.credential, bucketSettingsPermission); got != tc.settings {
			t.Errorf("%s: settings = %v, want %v", tc.name, got, tc.settings)
		}
		if got := h.can(tc.credential, state.ObjectBucketPermissionWrite); got != tc.write {
			t.Errorf("%s: object write = %v, want %v", tc.name, got, tc.write)
		}
	}
}
