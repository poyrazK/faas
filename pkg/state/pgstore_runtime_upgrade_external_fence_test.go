package state_test

// adr: 708

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimefence"
	"github.com/onebox-faas/faas/pkg/state"
)

type externalFenceFixture struct {
	store   *state.PgStore
	pool    *pgxpool.Pool
	gateway state.RuntimeUpgradeGatewayRoster
	public  state.RuntimeUpgradePublicEdgeRoster
	old     state.RuntimeUpgradePublicEdgeMember
	key     ed25519.PrivateKey
	review  state.RuntimeUpgradeExternalFenceReview
	intent  runtimefence.Intent
}

func newExternalFenceFixture(t *testing.T) externalFenceFixture {
	t.Helper()
	s, pool, gateway, original := publicEdgeFixture(t)
	current := replacePublicSession(t, s, original)
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	authority := uuid.NewString()
	if _, err := s.ReviewRuntimeUpgradeExternalFenceAuthority(t.Context(), authority, pub); err != nil {
		t.Fatal(err)
	}
	var withdrawal string
	if err := pool.QueryRow(t.Context(), `SELECT id::text FROM runtime_upgrade_public_edge_withdrawals WHERE public_session_id=$1`, original.Members[0].SessionID).Scan(&withdrawal); err != nil {
		t.Fatal(err)
	}
	r := state.RuntimeUpgradeExternalFenceReview{ID: uuid.NewString(), WithdrawalID: withdrawal, AuthorityID: authority, GatewayRevision: gateway.Revision, PublicRevision: current.Revision, MachineID: strings.Repeat("a", 32), BootID: uuid.NewString(), ResourceID: "authority/hosts/node-a", ScopeSHA256: strings.Repeat("b", 64)}
	i, err := s.ReviewRuntimeUpgradeExternalFenceIntent(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	return externalFenceFixture{s, pool, gateway, current, original.Members[0], key, r, i}
}

func externalFenceClock(t *testing.T, pool *pgxpool.Pool) time.Time {
	t.Helper()
	var now time.Time
	if err := pool.QueryRow(t.Context(), `SELECT clock_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	return now
}

func signedExternalFence(t *testing.T, key ed25519.PrivateKey, intent runtimefence.Intent, at time.Time, edit func(*runtimefence.Claim)) []byte {
	t.Helper()
	digest, err := runtimefence.IntentDigest(intent)
	if err != nil {
		t.Fatal(err)
	}
	c := runtimefence.Claim{Version: 1, Contract: runtimefence.Contract, AuthorityID: intent.AuthorityID, ReceiptID: uuid.NewString(), IntentID: intent.ID, IntentSHA256: digest, Challenge: intent.Challenge, EnforcedAtMicros: at.UnixMicro(), IssuedAtMicros: at.UnixMicro()}
	if edit != nil {
		edit(&c)
	}
	payload, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(runtimefence.Envelope{Claim: c, Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, append([]byte(runtimefence.SignatureDomain), payload...)))})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func externalFencePending(t *testing.T, f externalFenceFixture) int {
	t.Helper()
	out := observePublicCoverage(t, f.store, f.public)
	return len(out.PendingWithdrawals)
}

func TestPgExternalFenceAuthorityPinsCanonicalKeyAndIrreversibleRevocation(t *testing.T) {
	f := newExternalFenceFixture(t)
	pub := f.key.Public().(ed25519.PublicKey)
	first, err := f.store.ReviewRuntimeUpgradeExternalFenceAuthority(t.Context(), f.review.AuthorityID, pub)
	if err != nil {
		t.Fatal(err)
	}
	pub[0] ^= 1
	first.PublicKey[0] ^= 1
	same, err := f.store.ReviewRuntimeUpgradeExternalFenceAuthority(t.Context(), f.review.AuthorityID, f.key.Public().(ed25519.PublicKey))
	if err != nil || !same.CreatedAt.Equal(first.CreatedAt) || !bytes.Equal(same.PublicKey, f.key.Public().(ed25519.PublicKey)) {
		t.Fatal("key alias or retry drift", same, err)
	}
	other, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id   string
		key  []byte
		want error
	}{
		{f.review.AuthorityID, other, state.ErrConflict},
		{uuid.NewString(), same.PublicKey, state.ErrConflict},
		{"bad", same.PublicKey, state.ErrInvalidArgument},
		{uuid.NewString(), make([]byte, 32), state.ErrInvalidArgument},
	} {
		if _, err := f.store.ReviewRuntimeUpgradeExternalFenceAuthority(t.Context(), tc.id, tc.key); !errors.Is(err, tc.want) {
			t.Fatal(err, tc.want)
		}
	}
	if err := f.store.RevokeRuntimeUpgradeExternalFenceAuthority(t.Context(), f.review.AuthorityID); err != nil {
		t.Fatal(err)
	}
	var firstRevoked, secondRevoked time.Time
	if err := f.pool.QueryRow(t.Context(), `SELECT revoked_at FROM runtime_upgrade_external_fence_authorities WHERE id=$1`, f.review.AuthorityID).Scan(&firstRevoked); err != nil {
		t.Fatal(err)
	}
	if err := f.store.RevokeRuntimeUpgradeExternalFenceAuthority(t.Context(), f.review.AuthorityID); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(t.Context(), `SELECT revoked_at FROM runtime_upgrade_external_fence_authorities WHERE id=$1`, f.review.AuthorityID).Scan(&secondRevoked); err != nil || !secondRevoked.Equal(firstRevoked) {
		t.Fatal("revocation refreshed", err)
	}
	if _, err := f.store.ReviewRuntimeUpgradeExternalFenceAuthority(t.Context(), f.review.AuthorityID, same.PublicKey); !errors.Is(err, state.ErrConflict) {
		t.Fatal(err)
	}
	for _, query := range []string{
		`UPDATE runtime_upgrade_external_fence_authorities SET public_key=$2 WHERE id=$1`,
		`UPDATE runtime_upgrade_external_fence_authorities SET revoked_at=NULL WHERE id=$1 AND public_key=$2`,
		`DELETE FROM runtime_upgrade_external_fence_authorities WHERE id=$1 AND public_key=$2`,
	} {
		if _, err := f.pool.Exec(t.Context(), query, f.review.AuthorityID, same.PublicKey); err == nil {
			t.Fatal("authority history mutated", query)
		}
	}
}

func TestPgExternalFenceIntentFreezesDatabaseWithdrawalAndExactReview(t *testing.T) {
	f := newExternalFenceFixture(t)
	if f.intent.SessionID != f.old.SessionID || f.intent.SlotID != f.old.SlotID || f.intent.ConfigSHA256 != f.old.ConfigSHA256 || f.intent.Challenge == "" || f.intent.CreatedAtMicros < 1 {
		t.Fatal("startup binding missing", f.intent)
	}
	got, err := f.store.ReviewRuntimeUpgradeExternalFenceIntent(t.Context(), f.review)
	if err != nil || got != f.intent {
		t.Fatal("review retry drift", got, err)
	}
	for name, edit := range map[string]func(*state.RuntimeUpgradeExternalFenceReview){
		"withdrawal": func(r *state.RuntimeUpgradeExternalFenceReview) { r.WithdrawalID = uuid.NewString() },
		"authority":  func(r *state.RuntimeUpgradeExternalFenceReview) { r.AuthorityID = uuid.NewString() },
		"gateway":    func(r *state.RuntimeUpgradeExternalFenceReview) { r.GatewayRevision = uuid.NewString() },
		"public":     func(r *state.RuntimeUpgradeExternalFenceReview) { r.PublicRevision = uuid.NewString() },
		"machine":    func(r *state.RuntimeUpgradeExternalFenceReview) { r.MachineID = strings.Repeat("c", 32) },
		"boot":       func(r *state.RuntimeUpgradeExternalFenceReview) { r.BootID = uuid.NewString() },
		"resource":   func(r *state.RuntimeUpgradeExternalFenceReview) { r.ResourceID = "another-host" },
		"scope":      func(r *state.RuntimeUpgradeExternalFenceReview) { r.ScopeSHA256 = strings.Repeat("c", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			r := f.review
			edit(&r)
			if _, err := f.store.ReviewRuntimeUpgradeExternalFenceIntent(t.Context(), r); err == nil {
				t.Fatal("intent retargeted")
			}
		})
	}
	for _, query := range []string{`UPDATE runtime_upgrade_external_fence_intents SET machine_id=repeat('d',32) WHERE id=$1`, `DELETE FROM runtime_upgrade_external_fence_intents WHERE id=$1`} {
		if _, err := f.pool.Exec(t.Context(), query, f.intent.ID); err == nil {
			t.Fatal("immutable intent mutated")
		}
	}
}

func TestPgExternalFenceReceiptResolvesHistoryWithoutReenrollmentOrLocalSeal(t *testing.T) {
	f := newExternalFenceFixture(t)
	if externalFencePending(t, f) != 1 {
		t.Fatal("withdrawal already resolved")
	}
	raw := signedExternalFence(t, f.key, f.intent, externalFenceClock(t, f.pool), nil)
	got, err := f.store.RecordRuntimeUpgradeExternalFenceReceipt(t.Context(), f.intent.ID, raw)
	if err != nil || got.IntentID != f.intent.ID || got.WithdrawalID != f.review.WithdrawalID || !bytes.Equal(got.Envelope, raw) {
		t.Fatal(got, err)
	}
	hash := sha256.Sum256(raw)
	if got.EnvelopeSHA256 != hex.EncodeToString(hash[:]) || got.ObservedAt.Before(got.IssuedAt) || externalFencePending(t, f) != 0 {
		t.Fatal("receipt not durable", got)
	}
	// Permanent historical proof still needs current startup activity; it does
	// not manufacture a live receiver or current-generation coverage.
	if out := observePublicCoverage(t, f.store, f.public); out.Status == "coverage_observed" {
		t.Fatal("receipt substituted for current activity", out)
	}
	for _, m := range f.public.Members {
		recordPublicActivity(t, f.store, m, state.RuntimeUpgradePublicEdgeActivity{Version: 1, Known: true})
	}
	if out := observePublicCoverage(t, f.store, f.public); out.Status != "coverage_observed" {
		t.Fatal("fresh current activity plus permanent receipt did not recover coverage", out)
	}
	got.Envelope[0] ^= 1
	if err := f.store.RevokeRuntimeUpgradeExternalFenceAuthority(t.Context(), f.review.AuthorityID); err != nil {
		t.Fatal(err)
	}
	f.public = replacePublicSession(t, f.store, f.public)
	retry, err := f.store.RecordRuntimeUpgradeExternalFenceReceipt(t.Context(), f.intent.ID, raw)
	if err != nil || !retry.ObservedAt.Equal(got.ObservedAt) || !bytes.Equal(retry.Envelope, raw) || externalFencePending(t, f) != 1 {
		t.Fatal("accepted permanent history refreshed or lost", retry, err)
	}
	if review, err := f.store.ReviewRuntimeUpgradeExternalFenceIntent(t.Context(), f.review); err != nil || review != f.intent {
		t.Fatal("accepted review retry lost", review, err)
	}
	for _, query := range []string{`UPDATE runtime_upgrade_external_fence_receipts SET observed_at=clock_timestamp() WHERE withdrawal_id=$1`, `DELETE FROM runtime_upgrade_external_fence_receipts WHERE withdrawal_id=$1`} {
		if _, err := f.pool.Exec(t.Context(), query, f.review.WithdrawalID); err == nil {
			t.Fatal("permanent receipt mutated")
		}
	}
	members := append([]state.RuntimeUpgradePublicEdgeMember(nil), f.public.Members...)
	members[0] = f.old
	if _, err := f.store.ReviewRuntimeUpgradePublicEdgeRoster(t.Context(), f.public.Revision, f.gateway.Revision, f.public.TopologySHA256, members); !errors.Is(err, state.ErrConflict) {
		t.Fatal("terminated session reenrolled", err)
	}
	var local int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM runtime_upgrade_public_edge_withdrawal_receipts`).Scan(&local); err != nil || local != 0 {
		t.Fatal("external proof forged local version", local, err)
	}
}

func TestPgExternalFenceReceiptRejectsAuthenticButRetargetedClaims(t *testing.T) {
	f := newExternalFenceFixture(t)
	now := externalFenceClock(t, f.pool)
	for name, edit := range map[string]func(*runtimefence.Intent){
		"session":   func(i *runtimefence.Intent) { i.SessionID = uuid.NewString() },
		"slot":      func(i *runtimefence.Intent) { i.SlotID = uuid.NewString() },
		"config":    func(i *runtimefence.Intent) { i.ConfigSHA256 = strings.Repeat("d", 64) },
		"host":      func(i *runtimefence.Intent) { i.MachineID = strings.Repeat("d", 32) },
		"epoch":     func(i *runtimefence.Intent) { i.BootID = uuid.NewString() },
		"scope":     func(i *runtimefence.Intent) { i.ScopeSHA256 = strings.Repeat("d", 64) },
		"challenge": func(i *runtimefence.Intent) { i.Challenge = uuid.NewString() },
	} {
		t.Run(name, func(t *testing.T) {
			i := f.intent
			edit(&i)
			raw := signedExternalFence(t, f.key, i, now, nil)
			if _, err := f.store.RecordRuntimeUpgradeExternalFenceReceipt(t.Context(), f.intent.ID, raw); !errors.Is(err, runtimefence.ErrUnverified) {
				t.Fatal("retargeted proof accepted", err)
			}
		})
	}
	for _, contract := range []string{"host_powered_off", "temporary_network_isolation", "selected_systemd_masks"} {
		raw := signedExternalFence(t, f.key, f.intent, now, func(c *runtimefence.Claim) { c.Contract = contract })
		if _, err := f.store.RecordRuntimeUpgradeExternalFenceReceipt(t.Context(), f.intent.ID, raw); !errors.Is(err, runtimefence.ErrUnverified) {
			t.Fatal(contract, err)
		}
	}
	_, wrong, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.RecordRuntimeUpgradeExternalFenceReceipt(t.Context(), f.intent.ID, signedExternalFence(t, wrong, f.intent, now, nil)); !errors.Is(err, runtimefence.ErrUnverified) {
		t.Fatal("foreign signing key accepted", err)
	}
	if externalFencePending(t, f) != 1 {
		t.Fatal("failed proof cleared history")
	}
}

func TestPgExternalFenceNewReceiptsRequireBothCurrentHeads(t *testing.T) {
	for _, head := range []string{"public", "internal"} {
		t.Run(head, func(t *testing.T) {
			f := newExternalFenceFixture(t)
			raw := signedExternalFence(t, f.key, f.intent, externalFenceClock(t, f.pool), nil)
			if head == "public" {
				f.public = replacePublicSession(t, f.store, f.public)
			} else {
				m := append([]state.RuntimeUpgradeGatewayMember(nil), f.gateway.Members...)
				m[0].SessionID = uuid.NewString()
				if _, err := f.store.ReviewRuntimeUpgradeGatewayRoster(t.Context(), f.gateway.Revision, m); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.store.RecordRuntimeUpgradeExternalFenceReceipt(t.Context(), f.intent.ID, raw); !errors.Is(err, state.ErrConflict) {
				t.Fatal("stale head accepted", err)
			}
			r := f.review
			r.ID = uuid.NewString()
			if _, err := f.store.ReviewRuntimeUpgradeExternalFenceIntent(t.Context(), r); !errors.Is(err, state.ErrConflict) {
				t.Fatal("fresh intent borrowed stale heads", err)
			}
			if got, err := f.store.ReviewRuntimeUpgradeExternalFenceIntent(t.Context(), f.review); err != nil || got != f.intent {
				t.Fatal("old intent history lost", err)
			}
		})
	}
}

func TestPgExternalFenceRevocationBlocksNewProofAndIntent(t *testing.T) {
	f := newExternalFenceFixture(t)
	raw := signedExternalFence(t, f.key, f.intent, externalFenceClock(t, f.pool), nil)
	if err := f.store.RevokeRuntimeUpgradeExternalFenceAuthority(t.Context(), f.review.AuthorityID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.RecordRuntimeUpgradeExternalFenceReceipt(t.Context(), f.intent.ID, raw); !errors.Is(err, state.ErrConflict) {
		t.Fatal("revoked issuer accepted", err)
	}
	r := f.review
	r.ID = uuid.NewString()
	if _, err := f.store.ReviewRuntimeUpgradeExternalFenceIntent(t.Context(), r); !errors.Is(err, state.ErrConflict) {
		t.Fatal("revoked issuer reviewed", err)
	}
	if externalFencePending(t, f) != 1 {
		t.Fatal("revocation cleared pending withdrawal")
	}
}

func TestPgExternalFenceConcurrentExactRetriesPreserveOneReceipt(t *testing.T) {
	f := newExternalFenceFixture(t)
	raw := signedExternalFence(t, f.key, f.intent, externalFenceClock(t, f.pool), nil)
	type result struct {
		receipt state.RuntimeUpgradeExternalFenceReceipt
		err     error
	}
	done := make(chan result, 8)
	for range 8 {
		go func() {
			r, e := f.store.RecordRuntimeUpgradeExternalFenceReceipt(t.Context(), f.intent.ID, raw)
			done <- result{r, e}
		}()
	}
	var first state.RuntimeUpgradeExternalFenceReceipt
	for n := range 8 {
		r := <-done
		if r.err != nil {
			t.Fatal(r.err)
		}
		if n == 0 {
			first = r.receipt
		} else if !reflect.DeepEqual(first, r.receipt) {
			t.Fatal("concurrent retry changed receipt")
		}
	}
	other := signedExternalFence(t, f.key, f.intent, externalFenceClock(t, f.pool), nil)
	if _, err := f.store.RecordRuntimeUpgradeExternalFenceReceipt(t.Context(), f.intent.ID, other); !errors.Is(err, state.ErrConflict) {
		t.Fatal("different signed receipt replaced history", err)
	}
}

func lockExternalFenceRow(t *testing.T, f externalFenceFixture, kind string) pgx.Tx {
	t.Helper()
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	query := `SELECT id FROM runtime_upgrade_public_edge_withdrawals WHERE id=$1 FOR UPDATE`
	id := f.review.WithdrawalID
	switch kind {
	case "head":
		query = `SELECT revision FROM runtime_upgrade_gateway_roster_head WHERE singleton FOR UPDATE`
		id = ""
	case "authority":
		query = `SELECT id FROM runtime_upgrade_external_fence_authorities WHERE id=$1 FOR UPDATE`
		id = f.review.AuthorityID
	}
	var ignored any
	if kind == "head" {
		err = tx.QueryRow(t.Context(), query).Scan(&ignored)
	} else {
		err = tx.QueryRow(t.Context(), query, id).Scan(&ignored)
	}
	if err != nil {
		t.Fatal(err)
	}
	return tx
}

func TestPgExternalFenceSamplesClockAfterAllLockWaits(t *testing.T) {
	for _, kind := range []string{"head", "withdrawal", "authority"} {
		t.Run(kind, func(t *testing.T) {
			f := newExternalFenceFixture(t)
			tx := lockExternalFenceRow(t, f, kind)
			at := externalFenceClock(t, f.pool).Add(250 * time.Millisecond)
			raw := signedExternalFence(t, f.key, f.intent, at, nil)
			done := make(chan error, 1)
			go func() {
				_, err := f.store.RecordRuntimeUpgradeExternalFenceReceipt(t.Context(), f.intent.ID, raw)
				done <- err
			}()
			waitPublicEdgeRead(t, f.pool, tx)
			time.Sleep(time.Until(at.Add(30 * time.Millisecond)))
			if err := tx.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal("clock sampled before lock wait", err)
			}
		})
	}
}

func TestPgExternalFenceWaitingReceiptSeesCommittedRevocation(t *testing.T) {
	f := newExternalFenceFixture(t)
	tx := lockExternalFenceRow(t, f, "authority")
	raw := signedExternalFence(t, f.key, f.intent, externalFenceClock(t, f.pool), nil)
	done := make(chan error, 1)
	go func() {
		_, err := f.store.RecordRuntimeUpgradeExternalFenceReceipt(t.Context(), f.intent.ID, raw)
		done <- err
	}()
	waitPublicEdgeRead(t, f.pool, tx)
	if _, err := tx.Exec(t.Context(), `UPDATE runtime_upgrade_external_fence_authorities SET revoked_at=clock_timestamp() WHERE id=$1`, f.review.AuthorityID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, state.ErrConflict) {
		t.Fatal("wait borrowed pre-revocation key", err)
	}
}

func TestPgExternalFenceLocalAndExternalReceiptsSerializeOnWithdrawal(t *testing.T) {
	for _, winner := range []string{"local", "external"} {
		t.Run(winner, func(t *testing.T) {
			f := newExternalFenceFixture(t)
			tx := lockExternalFenceRow(t, f, "withdrawal")
			raw := signedExternalFence(t, f.key, f.intent, externalFenceClock(t, f.pool), nil)
			done := make(chan error, 1)
			installed := false
			local := func() error {
				_, err := f.store.RepairRuntimeUpgradePublicEdgeWithdrawal(t.Context(), f.old, func(id string) (state.RuntimeUpgradePublicEdgeWithdrawalSnapshot, error) {
					installed = true
					return state.RuntimeUpgradePublicEdgeWithdrawalSnapshot{ID: id, FenceID: uuid.NewString(), Version: 1, Closed: true, Known: true}, nil
				})
				return err
			}
			if winner == "local" {
				go func() {
					_, err := f.store.RecordRuntimeUpgradeExternalFenceReceipt(t.Context(), f.intent.ID, raw)
					done <- err
				}()
				waitPublicEdgeRead(t, f.pool, tx)
				if _, err := tx.Exec(t.Context(), `INSERT INTO runtime_upgrade_public_edge_withdrawal_receipts VALUES($1,$2,1,true,true,0,clock_timestamp())`, f.review.WithdrawalID, uuid.NewString()); err != nil {
					t.Fatal(err)
				}
			} else {
				go func() { done <- local() }()
				waitPublicEdgeRead(t, f.pool, tx)
				var e runtimefence.Envelope
				if err := json.Unmarshal(raw, &e); err != nil {
					t.Fatal(err)
				}
				h := sha256.Sum256(raw)
				if _, err := tx.Exec(t.Context(), `INSERT INTO runtime_upgrade_external_fence_receipts VALUES($1,$2,$3,$4,$5,$6,$7,clock_timestamp())`, f.review.WithdrawalID, f.intent.ID, e.Claim.ReceiptID, raw, hex.EncodeToString(h[:]), time.UnixMicro(e.Claim.EnforcedAtMicros), time.UnixMicro(e.Claim.IssuedAtMicros)); err != nil {
					t.Fatal(err)
				}
			}
			if err := tx.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, state.ErrConflict) {
				t.Fatal("both receipt families accepted", err)
			}
			if winner == "external" && installed {
				t.Fatal("external proof invoked a competing local fence")
			}
			if externalFencePending(t, f) != 0 {
				t.Fatal("winner did not resolve withdrawal")
			}
		})
	}
}

func TestPgExternalFenceDatabaseRejectsMalformedReceiptAndStaleReview(t *testing.T) {
	f := newExternalFenceFixture(t)
	now := externalFenceClock(t, f.pool)
	raw := signedExternalFence(t, f.key, f.intent, now, nil)
	var e runtimefence.Envelope
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(raw)
	for _, tc := range []struct {
		name                       string
		enforced, issued, observed time.Time
		digest                     string
		withdrawal                 string
	}{
		{"before_review", time.UnixMicro(f.intent.CreatedAtMicros - 1), now, now, hex.EncodeToString(h[:]), f.review.WithdrawalID},
		{"future", now, now.Add(time.Hour), now.Add(time.Hour), hex.EncodeToString(h[:]), f.review.WithdrawalID},
		{"bad_digest", now, now, now, strings.Repeat("d", 64), f.review.WithdrawalID},
		{"wrong_withdrawal", now, now, now, hex.EncodeToString(h[:]), uuid.NewString()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.pool.Exec(t.Context(), `INSERT INTO runtime_upgrade_external_fence_receipts VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, tc.withdrawal, f.intent.ID, e.Claim.ReceiptID, raw, tc.digest, tc.enforced, tc.issued, tc.observed); err == nil {
				t.Fatal("invalid direct receipt accepted")
			}
		})
	}
	f.public = replacePublicSession(t, f.store, f.public)
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO runtime_upgrade_external_fence_receipts VALUES($1,$2,$3,$4,$5,$6,$6,clock_timestamp())`, f.review.WithdrawalID, f.intent.ID, e.Claim.ReceiptID, raw, hex.EncodeToString(h[:]), now); err == nil {
		t.Fatal("direct receipt bypassed head pin")
	}
}

func TestPgExternalFenceResolutionReleasesPendingCapacity(t *testing.T) {
	f := newExternalFenceFixture(t)
	for n := 1; n < api.RuntimeUpgradePublicEdgeWithdrawalLimit; n++ {
		f.public = replacePublicSession(t, f.store, f.public)
	}
	members := append([]state.RuntimeUpgradePublicEdgeMember(nil), f.public.Members...)
	members[0].SessionID = uuid.NewString()
	if _, err := f.store.ReviewRuntimeUpgradePublicEdgeRoster(t.Context(), f.public.Revision, f.gateway.Revision, f.public.TopologySHA256, members); !errors.Is(err, state.ErrConflict) {
		t.Fatal("unresolved capacity bypassed", err)
	}
	r := f.review
	r.ID = uuid.NewString()
	r.PublicRevision = f.public.Revision
	intent, err := f.store.ReviewRuntimeUpgradeExternalFenceIntent(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	raw := signedExternalFence(t, f.key, intent, externalFenceClock(t, f.pool), nil)
	if _, err := f.store.RecordRuntimeUpgradeExternalFenceReceipt(t.Context(), intent.ID, raw); err != nil {
		t.Fatal(err)
	}
	if externalFencePending(t, f) != api.RuntimeUpgradePublicEdgeWithdrawalLimit-1 {
		t.Fatal("resolved history still consumes capacity")
	}
	if _, err := f.store.ReviewRuntimeUpgradePublicEdgeRoster(t.Context(), f.public.Revision, f.gateway.Revision, f.public.TopologySHA256, members); err != nil {
		t.Fatal("capacity not released", err)
	}
}

func TestPgExternalFenceStaleIssuanceCannotBorrowOldObservationTime(t *testing.T) {
	f := newExternalFenceFixture(t)
	// Seed genuinely historical rows through the trusted database test fixture.
	// No production API permits backdating an administrative review.
	historical, session, withdrawal := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO runtime_upgrade_public_edge_rosters(revision,gateway_roster_revision,topology_sha256,slot_ids,public_sessions,config_sha256s) SELECT $1,gateway_roster_revision,topology_sha256,ARRAY[slot_ids[1]],ARRAY[$2::uuid],ARRAY[config_sha256s[1]] FROM runtime_upgrade_public_edge_rosters WHERE revision=$3`, historical, session, f.public.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO runtime_upgrade_public_edge_withdrawals(id,slot_id,public_session_id,config_sha256,roster_revision,created_at) VALUES($1,$2,$3,$4,$5,clock_timestamp()-interval '120 seconds')`, withdrawal, f.intent.SlotID, session, f.intent.ConfigSHA256, historical); err != nil {
		t.Fatal(err)
	}
	i := f.intent
	i.ID, i.WithdrawalID, i.SessionID, i.Challenge = uuid.NewString(), withdrawal, session, uuid.NewString()
	var created time.Time
	if err := f.pool.QueryRow(t.Context(), `INSERT INTO runtime_upgrade_external_fence_intents(id,withdrawal_id,authority_id,challenge,gateway_revision,public_revision,machine_id,boot_id,resource_id,scope_sha256,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,clock_timestamp()-interval '110 seconds') RETURNING created_at`, i.ID, i.WithdrawalID, i.AuthorityID, i.Challenge, i.GatewayRevision, i.PublicRevision, i.MachineID, i.BootID, i.ResourceID, i.ScopeSHA256).Scan(&created); err != nil {
		t.Fatal(err)
	}
	i.CreatedAtMicros = created.UnixMicro()
	issued := externalFenceClock(t, f.pool).Add(-70 * time.Second)
	raw := signedExternalFence(t, f.key, i, issued, nil)
	if _, err := f.store.RecordRuntimeUpgradeExternalFenceReceipt(t.Context(), i.ID, raw); !errors.Is(err, runtimefence.ErrUnverified) {
		t.Fatal("stale signed proof accepted", err)
	}
	var e runtimefence.Envelope
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(raw)
	// observed_at=issued_at passes time order but must not reset the age check.
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO runtime_upgrade_external_fence_receipts VALUES($1,$2,$3,$4,$5,$6,$6,$6)`, i.WithdrawalID, i.ID, e.Claim.ReceiptID, raw, hex.EncodeToString(h[:]), issued); err == nil {
		t.Fatal("direct receipt borrowed a backdated observation clock")
	}
	if externalFencePending(t, f) != 2 {
		t.Fatal("stale proof cleared historical withdrawal")
	}
}

func TestPgExternalFenceMigratedCloneInventoryRemainsComplete(t *testing.T) {
	f := newExternalFenceFixture(t)
	a, err := f.store.CreateAccount(t.Context(), "external-fence-inventory@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.store.CreateProject(t.Context(), state.Project{AccountID: a.ID, Slug: "fence-inventory"})
	if err != nil {
		t.Fatal(err)
	}
	coverage, err := f.store.ProjectEnvironmentCloneSchemaCoverage(t.Context(), a.ID, p.ID)
	if err != nil || !coverage.Known {
		t.Fatal("migration lacks clone inventory policies", coverage.Blockers, err)
	}
	policies, err := state.ProjectEnvironmentCloneSchemaPolicies()
	if err != nil || len(coverage.Tables) != len(policies) {
		t.Fatal("migrated table inventory incomplete", err)
	}
}

func TestPgExternalFenceFreezesEnvelopeBeforeDatabaseWait(t *testing.T) {
	f := newExternalFenceFixture(t)
	tx := lockExternalFenceRow(t, f, "withdrawal")
	raw := signedExternalFence(t, f.key, f.intent, externalFenceClock(t, f.pool), nil)
	want := bytes.Clone(raw)
	type result struct {
		receipt state.RuntimeUpgradeExternalFenceReceipt
		err     error
	}
	done := make(chan result, 1)
	go func() {
		r, err := f.store.RecordRuntimeUpgradeExternalFenceReceipt(t.Context(), f.intent.ID, raw)
		done <- result{r, err}
	}()
	waitPublicEdgeRead(t, f.pool, tx)
	raw[0] = '!'
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	r := <-done
	if r.err != nil || !bytes.Equal(r.receipt.Envelope, want) {
		t.Fatal("caller changed evidence during lock wait", r.err)
	}
}
