package tasktoolbinding_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	b "github.com/onebox-faas/faas/pkg/tasktoolbinding"
)

func fixture(t *testing.T) (b.Record, time.Time) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	s := api.HostedTaskSourceIntent{RequesterKeyID: uuid.NewString(), AccountID: uuid.NewString(), ID: strings.Repeat("a", 64), IntentSHA256: strings.Repeat("b", 64), ReviewSHA256: strings.Repeat("c", 64), SourceState: "retained", ReviewStatus: "approved", ExpiresAt: now.Add(api.HostedTaskSourceRetention)}
	in := api.HostedTaskBindingSelection{IntentSHA256: s.IntentSHA256, ReviewSHA256: s.ReviewSHA256, Namespace: "tasks:test", WebDatabaseRef: "secret:WEB_DB", WorkerDatabaseRef: "secret:WORKER_DB", MigrationDatabaseRef: "secret:MIGRATION_DB", ObserverDatabaseRef: "secret:OBSERVER_DB"}
	p := b.Policy{WebDatabaseEnv: "WEB_DB", WorkerDatabaseEnv: "WORKER_DB", WebOwnerEnv: "OWNER_KEY", WorkerOwnerEnv: "OWNER_KEY", WebNamespaceEnv: "NAMESPACE", WorkerNamespaceEnv: "NAMESPACE", OwnerKeyID: "default"}
	var pins []b.Pin
	for _, role := range []string{"web_database", "worker_database", "migration_database", "observer_database", "web_owner", "worker_owner"} {
		pins = append(pins, b.Pin{Role: role, AppID: uuid.NewString(), Key: "KEY", SecretVersion: 1, CipherSHA256: strings.Repeat("d", 64)})
	}
	r, err := b.New(s, in, p, pins, uuid.NewString(), now)
	if err != nil {
		t.Fatal(err)
	}
	return r, now
}

func TestBindingClaimReplayAndFixedEvidenceDeadline(t *testing.T) {
	r, now := fixture(t)
	originalExpiry := r.Public.ExpiresAt
	digest := r.Public.BindingSHA256
	claim := uuid.NewString()
	r, err := b.Mutate(r, "claim", digest, claim, nil, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	second := uuid.NewString()
	again, err := b.Mutate(r, "claim", digest, second, nil, now.Add(2*time.Second))
	if err != nil || again.Claim != claim || again.Public.Status != "verifying" {
		t.Fatalf("claim replay: %+v %v", again, err)
	}
	if _, err := b.Mutate(r, "record", digest, second, b.Results("passed"), now.Add(3*time.Second)); !errors.Is(err, b.ErrConflict) {
		t.Fatalf("wrong claim accepted: %v", err)
	}
	r, err = b.Mutate(r, "record", digest, claim, b.Results("passed"), now.Add(3*time.Second))
	if err != nil || r.Public.Status != "passed" {
		t.Fatalf("completion: %+v %v", r.Public, err)
	}
	if r.Public.ReadyToApply || r.Public.BindingsVerified || !r.Public.OwnerAuthorizationRequired || !r.Public.ExpiresAt.Equal(originalExpiry) {
		t.Fatal("probe conferred authority or renewed source deadline")
	}
	deadline := *r.Public.ValidUntil
	checked := *r.Public.CheckedAt
	r, err = b.Mutate(r, "record", digest, claim, b.Results("failed"), now.Add(4*time.Second))
	if err != nil || !r.Public.ValidUntil.Equal(deadline) || !r.Public.CheckedAt.Equal(checked) || r.Public.Status != "passed" {
		t.Fatal("lost-response repeat rewrote evidence")
	}
	if b.View(r.Public, deadline).Status != "expired" {
		t.Fatal("evidence stayed current after its deadline")
	}
}

func TestBindingRevocationAndExpiryFenceInFlightProbe(t *testing.T) {
	for _, action := range []string{"revoke", "expire"} {
		t.Run(action, func(t *testing.T) {
			r, now := fixture(t)
			digest := r.Public.BindingSHA256
			claim := uuid.NewString()
			r, err := b.Mutate(r, "claim", digest, claim, nil, now.Add(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			finish := now.Add(3 * time.Second)
			want := "revoked"
			if action == "revoke" {
				r, err = b.Mutate(r, "revoke", digest, "", nil, now.Add(2*time.Second))
				if err != nil {
					t.Fatal(err)
				}
			} else {
				finish = r.Public.ExpiresAt
				want = "expired"
			}
			r, err = b.Mutate(r, "record", digest, claim, b.Results("passed"), finish)
			if err != nil || r.Public.Status != want || r.Public.CheckedAt != nil || r.Public.ValidUntil != nil {
				t.Fatalf("in-flight probe revived %s: %+v %v", want, r.Public, err)
			}
		})
	}
}

func TestBindingReceiptsRejectAuthorityAndAlteredPins(t *testing.T) {
	r, _ := fixture(t)
	for _, change := range []func(*api.HostedTaskBindingVerification){func(p *api.HostedTaskBindingVerification) { p.ReadyToApply = true }, func(p *api.HostedTaskBindingVerification) { p.BindingsVerified = true }, func(p *api.HostedTaskBindingVerification) { p.OwnerAuthorizationRequired = false }, func(p *api.HostedTaskBindingVerification) { p.Status = "passed" }, func(p *api.HostedTaskBindingVerification) { p.AccountID = uuid.NewString() }} {
		p := r.Public
		change(&p)
		if b.ValidateReceipt(p, r.Public.AccountID, r.Public.IntentID) == nil {
			t.Fatal("authority-bearing or cross-account receipt accepted")
		}
	}
	r.Pins[0].SecretVersion++
	if b.ValidateRecord(r) == nil {
		t.Fatal("changed credential version retained old binding digest")
	}
}

func TestBindingEvidenceCannotOutliveSource(t *testing.T) {
	r, now := fixture(t)
	digest := r.Public.BindingSHA256
	claim := uuid.NewString()
	r, err := b.Mutate(r, "claim", digest, claim, nil, r.Public.ExpiresAt.Add(-20*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	r, err = b.Mutate(r, "record", digest, claim, b.Results("passed"), now.Add(api.HostedTaskSourceRetention-10*time.Second))
	if err != nil || !r.Public.ValidUntil.Equal(r.Public.ExpiresAt) {
		t.Fatalf("evidence outlived source: %+v %v", r.Public, err)
	}
}
