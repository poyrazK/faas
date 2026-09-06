// Package rekey — background re-seal of app_secrets rows (ADR-089 PR-A).
//
// ADR-057 shipped host-key rotation with a 30-day overlap window:
// pkg/secretbox.OpenMulti unseals envelopes sealed under either the
// current or the previous host identity. The overlap window gives
// every customer envelope a 30-day grace period to be naturally
// re-sealed (any PUT during the overlap seals under the new key).
//
// But for customers who don't touch their secrets during the
// overlap, the envelopes stay sealed under the previous identity
// indefinitely. After the operator runs
// `gregale host-age prune-previous`, those envelopes become
// unreadable — the row's ciphertext opens against an identity the
// daemon no longer has.
//
// The rekey package closes that gap by walking app_secrets in
// order, unsealing each row with the loaded identity set
// (OpenMulti semantics — accepts current or previous), and
// re-sealing the plaintext under the CURRENT identity. After
// Replayer completes, every row has kid = current and unseals
// under current only — forward secrecy on the historical
// ciphertext set is restored.
//
// Operator opt-in: FAAS_REKEY_ENABLED=true|false. Default false
// preserves the v1 behaviour for compliance regimes that want
// historical recipient preservation (ADR-057 v2 follow-up #2).
//
// Rate-limited: Replayer.Run paces itself at RowsPerSecond
// (default 100) so a box with 50,000 sealed envelopes doesn't
// spike CPU at startup. Rate is configurable via RekeyConfig for
// tests.
//
// Idempotent: Run twice is a no-op the second time. Rows whose
// kid already matches the current identity are skipped (no
// re-seal, no audit emit). Safe to restart mid-walk — the
// progress callback is called at the end of each batch and the
// caller persists LastID for crash recovery.
package rekey

import (
	"context"
	"errors"
	"fmt"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// RekeyConfig paces the rekey walk. Defaults are tuned for the
// reference node's 50,000-row workload; smaller / larger boxes
// can override via the cmd/apid CLI flag.
type RekeyConfig struct {
	// RowsPerSecond caps the unseal+re-seal rate. Default 100.
	// The walk paces itself by sleeping between batches; under-
	// estimating the cap is harmless, over-estimating causes
	// CPU spikes that compete with wake-path latency.
	RowsPerSecond int
	// BatchSize is the rows-per-transaction unit. Default 50. A
	// failed batch (one bad row) aborts the transaction; the
	// Replayer logs the failure, increments Progress.Failed, and
	// continues with the next batch.
	BatchSize int
	// OpenTimeout caps the per-row unseal attempt. Default 5s.
	// Most rows unseal in <1ms on the reference node; the cap
	// exists to prevent a stuck row from blocking the whole walk.
	OpenTimeout time.Duration
}

// DefaultRekeyConfig is the production default. Tests construct
// their own RekeyConfig to drive fast / deterministic walks.
var DefaultRekeyConfig = RekeyConfig{
	RowsPerSecond: 100,
	BatchSize:     50,
	OpenTimeout:   5 * time.Second,
}

// RekeyProgress is the per-batch snapshot emitted via the
// progress callback. Counters are cumulative across the walk.
type RekeyProgress struct {
	Total   int    // rows visited across all batches so far
	Rekeyed int    // rows successfully re-sealed under current
	Skipped int    // rows whose kid already matched current (no-op)
	Failed  int    // rows that errored (bad ciphertext, timeout, etc.)
	LastID  string // last (account_id, app_id, scope, key) tuple visited; for crash recovery
}

// Replayer walks app_secrets and re-seals rows under the current
// host identity. Construct one at daemon startup when
// FAAS_REKEY_ENABLED=true; call Run() in a goroutine and pass
// progress to the operator status endpoint
// (GET /v1/admin/secrets/rekey-progress).
//
// Replayer is single-use: Run() consumes the supplied context.
// Construct a fresh Replayer after a daemon restart; the
// persisted RekeyProgress.LastID lets the operator resume the
// walk from where the previous incarnation left off.
//
// WALK MODEL (ADR-089 crash-safety invariant):
//
//	p.LastID advances PER-ROW before any unseal/seal/persist
//	attempt. pgstore.ListAppSecretsForRekey uses COMPOSITE
//	greater-than-or-equal on (account_id, app_id, key) — paired
//	with a per-run seen-set that drops duplicates. This gives
//	the "walk every row at least once per run, retry failures
//	only across separate daemon runs" semantic.
//
//	CRITICAL: with the per-row cursor advance + ≥ fence +
//	seen-set dedupe, a row whose persist fails (or whose
//	unseal/re-seal throws) is RE-FETCHED on the next run —
//	not silently skipped. After `gregale host-age
//	prune-previous` the previous-key envelope would otherwise
//	become permanently unreadable on a skipped row.
type Replayer struct {
	store      Store
	active     *age.X25519Recipient
	identities []*age.X25519Identity // current + previous for OpenMulti
	currentKid string
	// hostHMACKey is the per-host 32-byte key that secretbox.ValueFingerprint
	// uses to compute the value_hash stamped on every re-Seal row.
	// Loaded once at construction (the same posture apid's
	// cmd/apid/main.go::loadHostHMACKey uses); the key is per-host,
	// not per-row, so rotation is a host-level event, not a
	// row-level event (see ADR-117 D1 + the §Open questions entry
	// on HMAC key rotation). A nil/empty key is rejected at
	// construction time so a misconfigured rekey pass cannot
	// silently write value_hash = ''.
	hostHMACKey []byte
	cfg         RekeyConfig
}

// Store is the narrow interface pkg/rekey needs from the
// platform-wide state.Store. Two methods: ListAppSecretsForRekey
// (paginated global walk) and ResealAppSecretWithKidAndValueHashInScope
// (re-seal + kid + value_hash stamp at the row's actual scope).
// Implemented by both pkg/state/pgstore.PgStore and pkg/state/memstore.MemStore — see
// ADR-089 PR-A for the rationale. Defined as an interface here so unit
// tests can supply a tiny in-memory fakeStore without dragging in the
// full state.Store surface (which is ~hundreds of methods).
//
// ADR-092 PR-B: the rekey path must use the scope-aware sibling
// ResealAppSecretWithKidAndValueHashInScope (not a customer upsert that
// could claim the wrong scope). After
// PR-A widened the PK to (app_id, scope, key), every prod/staging row
// has a unique address; re-sealing at the wrong scope would either
// (a) insert a brand-new default-scope row of the same key (leaving
// prod with stale ciphertext) or (b) silently overwrite an existing
// default-scope row of the same key with new-identity ciphertext. The
// row.Scope thread from ListAppSecretsForRekey is the only correct
// write target.
//
// ADR-117 PR-C: the maintenance sibling carries the value_hash, which is
// computed off the re-Seal plaintext, NOT the new ciphertext. The
// rekey pass is the only path that can reliably backfill
// value_hash for pre-PR-C rows — a one-shot backfill sweep at PR-C
// merge time would require unsealing every row, which is a hot
// operation on Scale-tier accounts. Lazy backfill on re-seal is
// the documented ADR posture.
type Store interface {
	ListAppSecretsForRekey(ctx context.Context, limit int, cursor string) ([]state.AppSecret, error)
	ResealAppSecretWithKidAndValueHashInScope(ctx context.Context, accountID, appID, scope, key, kid, valueHash string, ciphertext []byte) error
}

// New constructs a Replayer. identities is the OpenMulti slice
// (current first, previous second) loaded from
// /etc/faas/secrets/host.age{,.previous} via
// secretbox.LoadHostKeys(dir). The current identity's recipient
// is sealed into every row the Replayer re-stamps.
//
// hostHMACKey is the per-host 32-byte key that secretbox.ValueFingerprint
// uses to compute the value_hash stamped on every re-Seal row
// (ADR-117 PR-C). The rekey pass is the only path that can
// reliably backfill value_hash for pre-PR-C rows (a one-shot
// backfill sweep at PR-C merge time would require unsealing every
// row). The key is defensively copied so a caller-side mutation
// does not affect re-Seal hashes.
//
// cfg is copied by value; mutating the caller's struct after
// New() has no effect on the Replayer.
func New(
	store Store,
	identities []*age.X25519Identity,
	hostHMACKey []byte,
	cfg RekeyConfig,
) (*Replayer, error) {
	if store == nil {
		return nil, errors.New("rekey: nil store")
	}
	if len(identities) == 0 || identities[0] == nil {
		return nil, errors.New("rekey: empty identities or nil current")
	}
	if len(hostHMACKey) == 0 {
		// ADR-117 D1: a rekey pass that writes value_hash = ''
		// would silently degrade the env-diff discriminator. The
		// rekey worker boots from the same host.hmac.key apid
		// loads; an empty key is a deployment misconfiguration
		// that must surface at construction time, not at the
		// first row.
		return nil, errors.New("rekey: empty host HMAC key — refusing to run; load /etc/faas/secrets/host.hmac.key first")
	}
	kid, err := secretbox.IdentityFingerprint(identities)
	if err != nil {
		return nil, fmt.Errorf("rekey: kid: %w", err)
	}
	if cfg.RowsPerSecond <= 0 {
		cfg.RowsPerSecond = DefaultRekeyConfig.RowsPerSecond
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = DefaultRekeyConfig.BatchSize
	}
	if cfg.OpenTimeout <= 0 {
		cfg.OpenTimeout = DefaultRekeyConfig.OpenTimeout
	}
	// Defensive copy: callers can wipe their own slice after
	// New() without affecting the rekey pass.
	keyCopy := make([]byte, len(hostHMACKey))
	copy(keyCopy, hostHMACKey)
	return &Replayer{
		store:       store,
		active:      identities[0].Recipient(),
		identities:  identities,
		currentKid:  kid,
		hostHMACKey: keyCopy,
		cfg:         cfg,
	}, nil
}

// Run walks app_secrets in (account_id, app_id, key) order and
// re-seals every row whose kid != current under the current
// identity. The progress callback is called at the end of each
// batch with the cumulative snapshot; nil callback is allowed
// (no progress reporting).
//
// Returns nil on clean completion (every row visited). Returns
// ctx.Err() if the context is cancelled mid-walk. Per-row
// failures are recorded in Progress.Failed but do not abort the
// walk — a single bad ciphertext should not block the rest.
//
// cursor is the LastID from a previous interrupted Run; empty
// string starts from the beginning. The walk paginates by
// (account_id, app_id, key) ascending with cfg.BatchSize rows
// per page.
func (r *Replayer) Run(
	ctx context.Context,
	cursor string,
	progress func(RekeyProgress),
) error {
	if progress == nil {
		progress = func(RekeyProgress) {}
	}
	// Crash-safety model (ADR-089). Two invariants:
	//
	//   1. Cursor pin: p.LastID is the cursor of the FIRST
	//      row that failed in the current Run (if any), held
	//      for the rest of Run. On daemon restart, the next
	//      Run re-fetches the pinned row (via the >= fence)
	//      and re-attempts.
	//
	//   2. Seen-set: in-Run dedupe of rows the previous batch
	//      already processed. Without this the per-row cursor
	//      advance + >= fence would loop forever on a clean
	//      batch (the cursor row keeps re-matching >=). The
	//      seen-set lets the walk terminate.
	//
	// These two together ensure a row whose persist fails is
	// NEVER silently skipped — after `gregale host-age
	// prune-previous` the previous-key envelope would
	// otherwise become permanently unreadable.
	var p RekeyProgress
	p.LastID = cursor
	seen := make(map[string]struct{})
	// pinnedCursor holds the cursor of the FIRST row in this
	// Run that failed; once set, we never advance past it.
	// On Run completion p.LastID == pinnedCursor, signalling
	// "the operator needs to retry from this point".
	var pinnedCursor string

	// Pace the walk: cfg.RowsPerSecond rows/sec. We sleep at the
	// bottom of each batch loop so a small cfg.BatchSize doesn't
	// cause us to spin between rows.
	batchDelay := time.Duration(float64(time.Second) * float64(r.cfg.BatchSize) / float64(r.cfg.RowsPerSecond))

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		batch, err := r.store.ListAppSecretsForRekey(ctx, r.cfg.BatchSize, p.LastID)
		if err != nil {
			return fmt.Errorf("rekey: list batch: %w", err)
		}

		// Filter the batch against the in-Run seen-set so a
		// row the previous batch already processed doesn't get
		// re-processed here. The >= fence always returns the
		// cursor row + later rows; the seen-set drops the
		// cursor row on subsequent batches so the walk
		// terminates when the table has been fully visited.
		var fresh []state.AppSecret
		for _, row := range batch {
			ck := encodeCursor(row.AccountID, row.AppID, row.Scope, row.Key)
			if _, ok := seen[ck]; ok {
				continue
			}
			fresh = append(fresh, row)
		}
		if len(fresh) == 0 {
			progress(p)
			return nil
		}

		var batchLastSuccessCursor string

		for _, row := range fresh {
			p.Total++
			rowCursor := encodeCursor(row.AccountID, row.AppID, row.Scope, row.Key)
			seen[rowCursor] = struct{}{}

			if row.Kid == r.currentKid {
				// Already under current identity — skip.
				p.Skipped++
				// Last-success-row tracking for the
				// post-batch cursor decision.
				batchLastSuccessCursor = rowCursor
				continue
			}

			// Unseal with the loaded identity set (OpenMulti
			// semantics — accepts current or previous).
			openCtx, cancel := context.WithTimeout(ctx, r.cfg.OpenTimeout)
			_ = openCtx // Open doesn't accept a ctx; cancel()
			// is the only handle we have today.
			env, openErr := secretbox.OpenMulti(r.identities, row.Ciphertext)
			cancel()
			if openErr != nil {
				p.Failed++
				if pinnedCursor == "" {
					pinnedCursor = rowCursor
				}
				continue
			}

			// ADR-117 PR-C: value_hash computed over the
			// PLAINTEXT (env[row.Key]), NOT the new ciphertext
			// that Seal produces below. age X25519 +
			// ChaCha20-Poly1305 is probabilistically
			// non-deterministic — a ciphertext-derived hash
			// would diverge for every row. The plaintext
			// variant produces the "same hash across scopes =
			// same plaintext" property the env-diff
			// endpoint relies on. The key is loaded once at
			// New() and is per-host (not per-row).
			plaintext, ok := env[row.Key]
			if !ok {
				// Corrupted envelope: the seal-time key is
				// missing from the unsealed map. The
				// envelope Validate() at Seal time would
				// have caught this — getting here means the
				// row was sealed by a different code path
				// (pre-ADR-089) or the ciphertext was
				// tampered. Treat as a row failure and
				// continue (do NOT skip without stamping
				// value_hash — the env-diff endpoint
				// relies on every re-Seal row having a
				// stamped value_hash).
				p.Failed++
				if pinnedCursor == "" {
					pinnedCursor = rowCursor
				}
				continue
			}
			valueHash, vhErr := secretbox.ValueFingerprint([]byte(plaintext), r.hostHMACKey)
			if vhErr != nil {
				// Empty plaintext: same posture as
				// sealAndPersist's empty-input guard
				// (5xx, do not skip — a row with
				// value_hash = '' silently degrades the
				// env-diff discriminator).
				p.Failed++
				if pinnedCursor == "" {
					pinnedCursor = rowCursor
				}
				continue
			}

			// Re-seal under current identity.
			sealed, sealErr := secretbox.Seal(r.active, env)
			if sealErr != nil {
				p.Failed++
				if pinnedCursor == "" {
					pinnedCursor = rowCursor
				}
				continue
			}

			// Persist. ADR-089 D4: kid column is stamped
			// alongside the new ciphertext. ADR-092 PR-B: the
			// write target is the row's actual scope
			// (row.Scope, populated by ListAppSecretsForRekey),
			// NOT DefaultEnvScope — pre-PR-B this call used
			// UpsertAppSecretWithKid which delegated to
			// DefaultEnvScope and silently dropped prod/staging
			// rows (or corrupted a default-scope sibling of the
			// same key).
			//
			// ADR-117 PR-C: value_hash is stamped on every
			// re-Seal row so the rekey pass is the lazy
			// backfill path for pre-PR-C rows. The
			// pre-PR-C empty value_hash only persists for
			// rows the rekey pass skipped (kid == currentKid
			// or OpenMulti failed).
			if err := r.store.ResealAppSecretWithKidAndValueHashInScope(ctx, row.AccountID, row.AppID, row.Scope, row.Key, r.currentKid, valueHash, sealed); err != nil {
				p.Failed++
				if pinnedCursor == "" {
					pinnedCursor = rowCursor
				}
				continue
			}
			p.Rekeyed++
			batchLastSuccessCursor = rowCursor
		}

		// POST-BATCH CURSOR.
		//
		// pinnedCursor takes precedence: if ANY row in this
		// Run has failed, we leave the cursor pinned to the
		// first failure so the next Run re-attempts from
		// there. Resume (>=) brings back the pinned row
		// specifically; rows past it are picked up normally.
		//
		// When the walk is fully clean, cursor advances past
		// the last successfully-processed row in this batch.
		if pinnedCursor == "" && batchLastSuccessCursor != "" {
			p.LastID = batchLastSuccessCursor
		} else if pinnedCursor != "" {
			p.LastID = pinnedCursor
		}

		progress(p)

		// Pace the walk between batches.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(batchDelay):
		}
	}
}

// encodeCursor serialises the (account_id, app_id, scope, key)
// tuple into a LastID string. Returns
// "<account_id>|<app_id>|<scope>|<key>".
//
// ADR-092 PR-A: widened from 3-tuple to 4-tuple to thread scope
// alongside the other PK columns of app_secrets. Crash-recovery
// cursor pins for in-flight Replayer instances whose LastID was
// previously persisted in the pre-PR form — the pgstore
// ListAppSecretsForRekey cursor decoder (pkg/state/pgstore.go
// :10220-10260) lazy-falls-back: a 3-segment cursor is treated
// as scope='default' for the first batch of the resumed walk;
// the operator's first post-rollout Run upgrades the cursor to
// 4-segment form via encodeCursor and the fallback is no longer
// reached. The on-disk shape of kid, ciphertext, and the secret
// rows themselves is unchanged — only the cursor wire format
// gains one more component.
//
// Note on the original doc: "Uses '\x00' as separator" was a
// stale note — the actual implementation has always used '|'.
// The literal value matters for backend-side split_part parsers
// (the pgstore decoder is shared with the lazy-3→4 fallback
// path), so '|' is preserved here.
func encodeCursor(accountID, appID, scope, key string) string {
	return accountID + "|" + appID + "|" + scope + "|" + key
}
