// adr: 1027 — a major upgrade is a journaled compute change of the major
// alone: it lands like a resize, replays by request id, and a provider's
// final rejection ends it failed with the database back on its old major.
package managedpostgres

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func upgradeFixture(t *testing.T, majorUpgrade bool) (*Service, *MemoryStore, *resizeTestProvider, Database) {
	t.Helper()
	service, store, provider, database, _ := resizeFixtureWith(t, func(c *Capabilities) {
		c.PostgresMajors, c.MajorUpgrade, c.ClassResize = []int{16, 17, 18}, majorUpgrade, true
	})
	return service, store, provider, database
}

func upgradeRequest(d Database, major int) UpgradeMajorRequest {
	return UpgradeMajorRequest{AccountID: d.AccountID, DatabaseID: d.ID, RequestID: uuid.NewString(), PostgresMajor: major}
}

func TestUpgradeMajorLandsAndReplays(t *testing.T) {
	service, _, provider, database := upgradeFixture(t, true)
	request := upgradeRequest(database, 18)
	operation, err := service.UpgradeMajor(t.Context(), request)
	if err != nil || !operation.MajorUpgrade() || operation.TargetSpec().PostgresMajor != 18 || operation.TargetSpec().Class != database.Spec.Class ||
		operation.State != ResizePending {
		t.Fatalf("intent: %+v %v", operation, err)
	}
	result, err := service.Reconcile(t.Context(), database.AccountID, database.ID)
	if err != nil || result.State != StateReady || result.Spec.PostgresMajor != 18 || provider.patches != 1 {
		t.Fatalf("completion: %+v %v", result, err)
	}
	replayed, err := service.UpgradeMajor(t.Context(), request)
	if err != nil || replayed.ID != operation.ID || replayed.State != ResizeSucceeded {
		t.Fatalf("replay: %+v %v", replayed, err)
	}
	other := request
	other.PostgresMajor = 17
	if _, err := service.UpgradeMajor(t.Context(), other); !errors.Is(err, ErrConflict) {
		t.Fatalf("request id reused for another major: %v", err)
	}
	// Upgrades, resizes and policy changes each read only their own kind.
	if got, err := service.GetMajorUpgrade(t.Context(), database.AccountID, database.ID, request.RequestID); err != nil || got.ID != operation.ID {
		t.Fatalf("upgrade view: %+v %v", got, err)
	}
	if _, err := service.GetResize(t.Context(), database.AccountID, database.ID, request.RequestID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an upgrade read as a resize: %v", err)
	}
	if _, err := service.GetComputePolicyChange(t.Context(), database.AccountID, database.ID, request.RequestID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an upgrade read as a policy change: %v", err)
	}
	resize := resizeRequest(result)
	if _, err := service.Resize(t.Context(), resize); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetMajorUpgrade(t.Context(), database.AccountID, database.ID, resize.RequestID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a resize read as an upgrade: %v", err)
	}
}

func TestUpgradeMajorRejectedEndsFailed(t *testing.T) {
	for _, code := range []string{"upgrade_refused", "upgrade_failed"} {
		t.Run(code, func(t *testing.T) {
			service, _, provider, database := upgradeFixture(t, true)
			provider.reject = code
			request := upgradeRequest(database, 18)
			if _, err := service.UpgradeMajor(t.Context(), request); err != nil {
				t.Fatal(err)
			}
			result, err := service.Reconcile(t.Context(), database.AccountID, database.ID)
			if err != nil || result.State != StateReady || result.Spec != database.Spec || result.ObservedGeneration != result.DesiredGeneration ||
				result.DesiredGeneration != database.DesiredGeneration+1 {
				t.Fatalf("after the rejection: %+v %v", result, err)
			}
			got, err := service.GetMajorUpgrade(t.Context(), database.AccountID, database.ID, request.RequestID)
			if err != nil || got.State != ResizeFailed || got.ErrorCode != code || got.CompletedAt.IsZero() {
				t.Fatalf("failed upgrade: %+v %v", got, err)
			}
			// The outcome is final: the same request reports it, and a new one
			// may try again.
			calls := provider.calls
			if again, err := service.UpgradeMajor(t.Context(), request); err != nil || again.State != ResizeFailed || provider.calls != calls {
				t.Fatalf("replay of a failed upgrade: %+v %v", again, err)
			}
			provider.reject, provider.applied = "", false
			if _, err := service.UpgradeMajor(t.Context(), upgradeRequest(result, 18)); err != nil {
				t.Fatalf("a new upgrade after a failed one: %v", err)
			}
		})
	}
}

func TestUpgradeMajorAdmission(t *testing.T) {
	for _, tc := range []struct {
		name         string
		majorUpgrade bool
		major        int
		want         error
	}{
		{"same major", true, 17, ErrInvalid},
		{"earlier major", true, 16, ErrInvalid},
		{"no major", true, 0, ErrInvalid},
		{"major the backend lacks", true, 19, ErrUnsupported},
		{"backend cannot upgrade", false, 18, ErrUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, _, provider, database := upgradeFixture(t, tc.majorUpgrade)
			if _, err := service.UpgradeMajor(t.Context(), upgradeRequest(database, tc.major)); !errors.Is(err, tc.want) {
				t.Fatalf("UpgradeMajor = %v, want %v", err, tc.want)
			}
			if provider.calls != 0 {
				t.Fatal("provider called for a refused upgrade")
			}
		})
	}
	// Resize never carries a major.
	service, _, _, database := upgradeFixture(t, true)
	sneaky := resizeRequest(database)
	sneaky.TargetPostgresMajor = 18
	if _, err := service.Resize(t.Context(), sneaky); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a resize with a major = %v", err)
	}
}

func TestUpgradeOperationValidity(t *testing.T) {
	_, _, _, database := upgradeFixture(t, true)
	base := ResizeOperation{ID: uuid.NewString(), AccountID: database.AccountID, DatabaseID: database.ID, BackendID: database.BackendID,
		BackendFingerprint: database.BackendFingerprint, ProviderResourceID: database.ProviderResourceID, DataResourceID: database.DataResourceID,
		SourceSpec: database.Spec, TargetClass: database.Spec.Class, TargetPostgresMajor: 18, Generation: 2, State: ResizePending, CreatedAt: database.CreatedAt}
	if !validResizeOperation(base) {
		t.Fatal("upgrade refused")
	}
	withClass, withStorage, withPolicy, down := base, base, base, base
	withClass.TargetClass = ClassBurstable
	withStorage.TargetStorageLimitBytes = database.Spec.StorageLimitBytes * 2
	withPolicy.PolicyChange = true
	down.TargetPostgresMajor = 16
	for name, op := range map[string]ResizeOperation{"with a class change": withClass, "with storage": withStorage, "with a policy change": withPolicy, "downgrade": down} {
		if validResizeOperation(op) {
			t.Errorf("upgrade %s accepted", name)
		}
	}
	if (&ChangeRejectedError{Code: "x"}).Error() == "" || !errors.Is(&ChangeRejectedError{Code: "x"}, ErrConflict) {
		t.Fatal("a rejection is no conflict")
	}
	single := Capabilities{PostgresMajors: []int{17}, ServiceClasses: []ServiceClass{ClassDevelopment}, Availability: []Availability{AvailabilitySingleZone},
		CredentialAccess: []CredentialAccess{CredentialReadWrite}, MajorUpgrade: true}
	if err := single.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a single-major backend claims upgrades: %v", err)
	}
}
