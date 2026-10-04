package globaldirectory

import (
	"bytes"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

func delegationKeys(t *testing.T) DelegationKeys {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return DelegationKeys{Identities: []*age.X25519Identity{identity}}
}
func TestDelegationSealIdentityRotationAndTamper(t *testing.T) {
	s := NewMemoryStore()
	app, targets := releaseSetup(t, s, uuid.NewString())
	old := delegationKeys(t)
	g, err := old.Mint(app, targets[0])
	if err != nil {
		t.Fatal(err)
	}
	token, err := old.Open(g)
	if err != nil || !api.ValidRegionalReleaseTokenFormat(token) || bytes.Contains(g.Sealed, []byte(token)) {
		t.Fatalf("seal: %v", err)
	}
	public, err := json.Marshal(g)
	if err != nil || bytes.Contains(public, []byte(token)) || bytes.Contains(public, g.Sealed) {
		t.Fatal("authority in public metadata")
	}
	fresh := delegationKeys(t)
	if _, err := fresh.Open(g); !errors.Is(err, ErrDelegationSealed) {
		t.Fatalf("missing old identity: %v", err)
	}
	fresh.Identities = append(fresh.Identities, old.Identities...)
	if got, err := fresh.Open(g); err != nil || got != token {
		t.Fatalf("rotation: %v", err)
	}
	sameInstant := copyDelegation(g)
	sameInstant.ExpiresAt = g.ExpiresAt.In(time.FixedZone("db", 3600))
	if _, err := fresh.Open(sameInstant); err != nil {
		t.Fatalf("equivalent timestamp: %v", err)
	}
	mutations := map[string]func(*Delegation){
		"grant":        func(g *Delegation) { g.ID = uuid.NewString() },
		"global app":   func(g *Delegation) { g.GlobalAppID = uuid.NewString() },
		"owner":        func(g *Delegation) { g.OwnerID = uuid.NewString() },
		"slug":         func(g *Delegation) { g.Slug = "other-app" },
		"origin":       func(g *Delegation) { g.APIBase = "https://other.example.com" },
		"cell":         func(g *Delegation) { g.Binding.CellID = uuid.NewString() },
		"generation":   func(g *Delegation) { g.Binding.CellGeneration++ },
		"regional app": func(g *Delegation) { g.Binding.RegionalAppID = uuid.NewString() },
		"expiry":       func(g *Delegation) { g.ExpiresAt = g.ExpiresAt.Add(time.Second) },
		"hash":         func(g *Delegation) { g.Hash[0] ^= 1 },
		"ciphertext":   func(g *Delegation) { g.Sealed[len(g.Sealed)-1] ^= 1 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			altered := copyDelegation(g)
			mutate(&altered)
			if out, err := fresh.Open(altered); !errors.Is(err, ErrDelegationSealed) || out != "" {
				t.Fatalf("tamper accepted: %v", err)
			}
		})
	}
	data, _ := json.Marshal(payload(g, token))
	wrong, err := secretbox.SealBytes(old.Identities[0].Recipient(), "other-purpose", data, api.GlobalDelegationPlaintextMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	g.Sealed = wrong
	if _, err := old.Open(g); !errors.Is(err, ErrDelegationSealed) {
		t.Fatalf("namespace confusion: %v", err)
	}
}

func delegationContract(t *testing.T, s Store, journal DelegationStore, owner string) {
	t.Helper()
	ctx := t.Context()
	app, targets := releaseSetup(t, s, owner)
	keys := delegationKeys(t)
	var wg sync.WaitGroup
	ids := make(chan string, 16)
	errs := make(chan error, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g, err := keys.Mint(app, targets[0])
			if err == nil {
				g, err = journal.EnsureDelegation(ctx, owner, app.Slug, g)
			}
			if err != nil {
				errs <- err
				return
			}
			ids <- g.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var id string
	for got := range ids {
		if id == "" {
			id = got
		}
		if id != got {
			t.Fatal("duplicate regional consent")
		}
	}
	g, err := journal.ReadDelegation(ctx, owner, app.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := keys.Open(g); err != nil {
		t.Fatalf("persisted seal: %v", err)
	}
	if _, err := journal.ReadDelegation(ctx, uuid.NewString(), app.ID, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign owner: %v", err)
	}
	if err := journal.SetDelegationStatus(ctx, owner, app.ID, id, DelegationActive); err != nil {
		t.Fatal(err)
	}
	work, err := journal.ClaimRecovery(ctx, uuid.NewString())
	if err != nil || len(work) != 1 {
		t.Fatalf("claim: %+v %v", work, err)
	}
	if again, err := journal.ClaimRecovery(ctx, uuid.NewString()); err != nil || len(again) != 0 {
		t.Fatalf("concurrent claim: %+v %v", again, err)
	}
	forged := work[0]
	forged.ClaimID = uuid.NewString()
	if err := journal.FinishRecovery(ctx, forged); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale finish: %v", err)
	}
	r, err := s.PlanRelease(ctx, owner, app.Slug, uuid.NewString(), releaseTestImage)
	if err != nil {
		t.Fatal(err)
	}
	work[0].CursorID = id
	if err := journal.FinishRecovery(ctx, work[0]); err != nil {
		t.Fatal(err)
	}
	wake, err := journal.ClaimRecovery(ctx, uuid.NewString())
	if err != nil || len(wake) != 1 || wake[0].ReleaseID != r.ReleaseID || wake[0].CursorID != id {
		t.Fatalf("lost wake/cursor: %+v %v", wake, err)
	}
	if err := s.DetachTarget(ctx, owner, app.Slug, targets[0].Region); err != nil {
		t.Fatal(err)
	}
	g, err = journal.ReadDelegation(ctx, owner, app.ID, id)
	if err != nil || g.Status != DelegationRevoking || len(g.Sealed) == 0 {
		t.Fatalf("retained revocation: %+v %v", g, err)
	}
	if err := journal.CheckDelegation(ctx, owner, app.ID, id); !errors.Is(err, ErrStaleCell) {
		t.Fatalf("detached authority: %v", err)
	}
	if err := journal.SetDelegationStatus(ctx, owner, app.ID, id, DelegationActive); err == nil {
		t.Fatal("revived delegation")
	}
	if err := journal.SetDelegationStatus(ctx, owner, app.ID, id, DelegationExpired); err == nil {
		t.Fatal("premature expiry")
	}
	if err := journal.SetDelegationStatus(ctx, owner, app.ID, id, DelegationRevoked); err != nil {
		t.Fatal(err)
	}
	g, err = journal.ReadDelegation(ctx, owner, app.ID, id)
	if err != nil || len(g.Sealed) != 0 || g.Status != DelegationRevoked {
		t.Fatalf("terminal secret retention: %+v %v", g, err)
	}
	if err := journal.SetDelegationStatus(ctx, owner, app.ID, id, DelegationActive); err == nil {
		t.Fatal("terminal consent revived")
	}
}
func TestMemoryDelegationContract(t *testing.T) {
	s := NewMemoryStore()
	delegationContract(t, s, s, uuid.NewString())
}

func TestDelegationLegacyAccountPayloadCompatibility(t *testing.T) {
	s := NewMemoryStore()
	app, targets := releaseSetup(t, s, uuid.NewString())
	keys := delegationKeys(t)
	g, err := keys.Mint(app, targets[0])
	if err != nil {
		t.Fatal(err)
	}
	token, err := keys.Open(g)
	if err != nil {
		t.Fatal(err)
	}
	legacy := map[string]any{"ID": g.ID, "GlobalAppID": g.GlobalAppID, "AccountID": g.OwnerID, "Slug": g.Slug, "APIBase": g.APIBase, "Token": token, "Binding": g.Binding, "ExpiresAt": g.ExpiresAt}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	g.Sealed, err = secretbox.SealBytes(keys.Identities[0].Recipient(), delegationNamespace, data, api.GlobalDelegationPlaintextMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := keys.Open(g); err != nil || got != token {
		t.Fatalf("legacy credential lost after owner rename: %v", err)
	}
}
