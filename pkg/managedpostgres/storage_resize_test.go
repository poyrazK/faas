// adr: 1027 — storage limits grow through the resize journal and never shrink.
package managedpostgres

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func storageResizeFixture(t *testing.T, classResize, storageResize bool) (*Service, *resizeTestProvider, Database) {
	t.Helper()
	service, _, provider, database, _ := resizeFixtureWith(t, func(c *Capabilities) {
		c.ClassResize, c.StorageResize = classResize, storageResize
	})
	return service, provider, database
}

func storageRequest(d Database, class ServiceClass, storage int64) ResizeDatabaseRequest {
	return ResizeDatabaseRequest{AccountID: d.AccountID, DatabaseID: d.ID, RequestID: uuid.NewString(), TargetClass: class, TargetStorageLimitBytes: storage}
}

func TestStorageResizeGrowsInPlaceAndReplays(t *testing.T) {
	service, provider, database := storageResizeFixture(t, false, true)
	grown := database.Spec.StorageLimitBytes * 2
	request := storageRequest(database, "", grown)
	operation, err := service.Resize(t.Context(), request)
	if err != nil || operation.TargetClass != database.Spec.Class || operation.TargetStorageLimitBytes != grown || operation.TargetSpec().StorageLimitBytes != grown {
		t.Fatalf("intent: %+v %v", operation, err)
	}
	result, err := service.Reconcile(t.Context(), database.AccountID, database.ID)
	if err != nil || result.State != StateReady || result.Spec.StorageLimitBytes != grown || result.Spec.Class != database.Spec.Class ||
		result.DataResourceID != database.DataResourceID || provider.patches != 1 {
		t.Fatalf("completion: %+v %v patches=%d", result, err, provider.patches)
	}
	// The same intent, spelled out in full, replays the operation.
	spelled := request
	spelled.TargetClass = database.Spec.Class
	replayed, err := service.Resize(t.Context(), spelled)
	if err != nil || replayed.ID != operation.ID || replayed.State != ResizeSucceeded {
		t.Fatalf("replay: %+v %v", replayed, err)
	}
	for name, changed := range map[string]ResizeDatabaseRequest{
		"other storage": {AccountID: request.AccountID, DatabaseID: request.DatabaseID, RequestID: request.RequestID, TargetStorageLimitBytes: grown * 2},
		"class added":   {AccountID: request.AccountID, DatabaseID: request.DatabaseID, RequestID: request.RequestID, TargetClass: ClassBurstable, TargetStorageLimitBytes: grown},
	} {
		if _, err := service.Resize(t.Context(), changed); !errors.Is(err, ErrConflict) {
			t.Errorf("%s: UUID reused with a different target: %v", name, err)
		}
	}
	if _, err := service.GetResize(t.Context(), database.AccountID, database.ID, request.RequestID); err != nil {
		t.Fatal("storage resize hidden from the resize view", err)
	}
}

func TestStorageAndClassResizeTogether(t *testing.T) {
	service, _, database := storageResizeFixture(t, true, true)
	grown := database.Spec.StorageLimitBytes + 1<<30
	if _, err := service.Resize(t.Context(), storageRequest(database, ClassBurstable, grown)); err != nil {
		t.Fatal(err)
	}
	result, err := service.Reconcile(t.Context(), database.AccountID, database.ID)
	if err != nil || result.Spec.Class != ClassBurstable || result.Spec.StorageLimitBytes != grown {
		t.Fatalf("completion: %+v %v", result, err)
	}
}

func TestStorageResizeAdmission(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		classResize, storage bool
		class                ServiceClass
		bytes                func(Spec) int64
		want                 error
	}{
		{"shrink", true, true, "", func(s Spec) int64 { return s.StorageLimitBytes / 2 }, ErrInvalid},
		{"negative", true, true, "", func(Spec) int64 { return -1 }, ErrInvalid},
		{"nothing named", true, true, "", func(Spec) int64 { return 0 }, ErrInvalid},
		{"above backend maximum", true, true, "", func(Spec) int64 { return 200 << 30 }, ErrUnsupported},
		{"backend cannot grow storage", true, false, "", func(s Spec) int64 { return s.StorageLimitBytes * 2 }, ErrUnsupported},
		{"class change without class resize", false, true, ClassBurstable, func(s Spec) int64 { return s.StorageLimitBytes * 2 }, ErrUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, provider, database := storageResizeFixture(t, tc.classResize, tc.storage)
			if _, err := service.Resize(t.Context(), storageRequest(database, tc.class, tc.bytes(database.Spec))); !errors.Is(err, tc.want) {
				t.Fatalf("Resize = %v, want %v", err, tc.want)
			}
			if provider.calls != 0 {
				t.Fatal("provider called for a refused resize")
			}
		})
	}
}

func TestResizeOperationRejectsStorageShrinkAndPolicyMix(t *testing.T) {
	_, _, _, database, _ := resizeFixture(t)
	base := ResizeOperation{ID: uuid.NewString(), AccountID: database.AccountID, DatabaseID: database.ID, BackendID: database.BackendID,
		BackendFingerprint: database.BackendFingerprint, ProviderResourceID: database.ProviderResourceID, DataResourceID: database.DataResourceID,
		SourceSpec: database.Spec, TargetClass: database.Spec.Class, Generation: 2, State: ResizePending, CreatedAt: database.CreatedAt}
	grow := base
	grow.TargetStorageLimitBytes = database.Spec.StorageLimitBytes + 1
	if !validResizeOperation(grow) {
		t.Fatal("growth refused")
	}
	shrink := base
	shrink.TargetStorageLimitBytes = database.Spec.StorageLimitBytes - 1
	policy := grow
	policy.PolicyChange = true
	fromUnlimited := grow
	fromUnlimited.SourceSpec.StorageLimitBytes = 0
	for name, op := range map[string]ResizeOperation{"shrink": shrink, "with a policy change": policy, "from no limit": fromUnlimited} {
		if validResizeOperation(op) {
			t.Errorf("%s accepted", name)
		}
	}
}
