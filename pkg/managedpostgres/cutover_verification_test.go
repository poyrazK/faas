// adr: 465 — durable read-only evidence, retries, and cancellation fencing.
package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

type cutoverProbe struct {
	calls []string
	run   func(context.Context, Binding, SealedCredential, Database) error
}

func (p *cutoverProbe) VerifyCredential(ctx context.Context, b Binding, c SealedCredential, d Database) error {
	p.calls = append(p.calls, b.ID)
	if p.run != nil {
		return p.run(ctx, b, c, d)
	}
	return nil
}
func preparedCutover(t *testing.T) (*CutoverService, *MemoryStore, Cutover, *time.Time, *bool) {
	t.Helper()
	s, store, _, _, _, now, enabled, r := cutoverFixture(t)
	c, err := s.Prepare(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		c, err = s.Reconcile(context.Background(), c.AccountID, c.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if c.State != CutoverPrepared {
		t.Fatal(c.State)
	}
	return s, store, c, now, enabled
}
func TestCutoverVerificationRetriesOnlyUnverifiedMembersAndRefreshes(t *testing.T) {
	ctx := context.Background()
	s, store, c, now, _ := preparedCutover(t)
	probe := &cutoverProbe{}
	s.verifier = probe
	probe.run = func(_ context.Context, b Binding, sealed SealedCredential, d Database) error {
		if b.DatabaseID != d.ID || !validSealedCredential(sealed) {
			t.Fatal("not probing staged target")
		}
		if len(probe.calls) == 2 {
			return ErrUnavailable
		}
		return nil
	}
	c, err := s.Verify(ctx, c.AccountID, c.ID)
	if err != nil || c.State != CutoverVerifying {
		t.Fatalf("request: %s %v", c.State, err)
	}
	if _, err = s.Reconcile(ctx, c.AccountID, c.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	c, err = store.GetCutover(ctx, c.AccountID, c.ID)
	if err != nil || c.Credentials[0].VerifiedAt.IsZero() || !c.Credentials[1].VerifiedAt.IsZero() || c.LastErrorCode != "credential_verification_failed" {
		t.Fatalf("partial evidence: %+v %v", c, err)
	}
	*now = c.RetryAt
	probe.run = nil
	c, err = s.Reconcile(ctx, c.AccountID, c.ID)
	if err != nil || !c.VerificationFresh(*now) || len(probe.calls) != 3 {
		t.Fatalf("resume: %s %v calls=%v", c.State, err, probe.calls)
	}
	for _, m := range c.Credentials {
		b, _ := store.GetBinding(ctx, c.AccountID, m.SourceBindingID)
		if b.DatabaseID != c.Source.ID {
			t.Fatal("verification switched binding")
		}
	}
	if c.VerificationFresh(now.Add(CutoverVerificationMaxAge + time.Second)) {
		t.Fatal("stale evidence remained fresh")
	}
	c, err = s.Verify(ctx, c.AccountID, c.ID)
	if err != nil || !c.VerifiedAt.IsZero() {
		t.Fatal("refresh did not reset evidence", err)
	}
	for _, m := range c.Credentials {
		if !m.VerifiedAt.IsZero() {
			t.Fatal("member evidence survived refresh")
		}
	}
	c, err = s.Reconcile(ctx, c.AccountID, c.ID)
	if err != nil || len(probe.calls) != 5 || !c.VerificationFresh(*now) {
		t.Fatal("refresh skipped members", err)
	}
}
func TestCutoverVerificationCancellationAndEnvelopeFence(t *testing.T) {
	for _, mode := range []string{"cancel", "lease", "envelope", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			s, store, c, now, _ := preparedCutover(t)
			s.verifier = &cutoverProbe{run: func(probeCtx context.Context, b Binding, _ SealedCredential, _ Database) error {
				switch mode {
				case "cancel":
					if _, err := s.Cancel(ctx, c.AccountID, c.ID); err != nil {
						t.Fatal(err)
					}
				case "lease":
					*now = now.Add(2 * time.Minute)
				case "envelope":
					store.mu.Lock()
					stored := store.cutovers[c.ID]
					for i, m := range stored.Credentials {
						if m.ID == b.ID {
							stored.Credentials[i].Sealed.Ciphertext = []byte("replaced")
						}
					}
					store.cutovers[c.ID] = stored
					store.mu.Unlock()
				case "deadline":
					<-probeCtx.Done()
				}
				return nil
			}}
			if mode == "deadline" {
				s.bindings.providerTimeout = time.Millisecond
			}
			_, err := s.Verify(ctx, c.AccountID, c.ID)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.Reconcile(ctx, c.AccountID, c.ID)
			if err == nil {
				t.Fatal("late or altered probe accepted")
			}
			current, _ := s.Get(ctx, c.AccountID, c.ID)
			if current.State == CutoverVerified || !current.VerifiedAt.IsZero() {
				t.Fatal("invalid evidence persisted")
			}
		})
	}
}
func TestCutoverVerificationGateAndTenantIsolation(t *testing.T) {
	ctx := context.Background()
	s, _, c, _, enabled := preparedCutover(t)
	s.verifier = &cutoverProbe{}
	if _, err := s.Verify(ctx, "other-account", c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	*enabled = false
	if _, err := s.Verify(ctx, c.AccountID, c.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err := s.Cancel(ctx, c.AccountID, c.ID); err != nil {
		t.Fatal("cleanup gate closed", err)
	}
}
func TestCutoverVerificationCrashRecoveryAndIdempotentRequest(t *testing.T) {
	ctx := context.Background()
	s, store, c, now, _ := preparedCutover(t)
	probe := &cutoverProbe{}
	s.verifier = probe
	c, err := s.Verify(ctx, c.AccountID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimCutover(ctx, c.AccountID, c.ID, "crashed-worker", *now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SaveCutoverVerification(ctx, claim, claim.Credentials[0], *now); err != nil {
		t.Fatal(err)
	}
	repeat, err := s.Verify(ctx, c.AccountID, c.ID)
	if err != nil || repeat.LeaseToken != claim.LeaseToken || repeat.Credentials[0].VerifiedAt.IsZero() {
		t.Fatal("duplicate request stole lease or evidence", err)
	}
	*now = now.Add(time.Minute + time.Second)
	c, err = s.Reconcile(ctx, c.AccountID, c.ID)
	if err != nil || !c.VerificationFresh(*now) || len(probe.calls) != 1 {
		t.Fatal("did not recover batch", err)
	}
	if err = store.SaveCutoverVerification(ctx, claim, claim.Credentials[1], *now); !errors.Is(err, ErrConflict) {
		t.Fatal("old worker survived", err)
	}
}
