package main

// internal_svc_minter.go — ADR-119 minting surface for the
// outbound Authorization: Bearer JWT schedd attaches to
// /v1/synthesize requests targeting apps whose
// public_auth_mode='internal_only' (issue #477 #4).
//
// Production keypair loading (PR-3 / ADR-125 fleet-wide signing
// key): schedd tries the cluster_signing_keys row in PG FIRST.
// Every box that joins the fleet reads the same cluster key, so
// a JWT minted by schedd on box A is verifiable by
// gatewayd-internal on box B — the cross-box audit F1+F20 fix.
//
// The cluster path requires the operator to have run
// `hostage-gen cluster-init` once (out of scope for PR-3; the
// minimal in-tree helper is pkg/state.PgStore.InsertClusterSigningKey
// + the SQL row shape at migrations/00351). Until then, or on a
// box whose host.age chain cannot unseal the cluster blob (the
// shared-host.age bootstrap mistake), the loader falls back to
// the per-host paths below.
//
// Per-host fallback (single-box dev + operator-migration window):
// the operator provisions the Ed25519 keypair at
// /etc/faas/secrets/internal-svc/schedd.ed25519 (override via
// FAAS_INTERNAL_SVC_KEY_PATH). The key is generated fresh on
// first boot if missing — a loud WARN log makes this visible so
// the operator can persist it to host.age. The corresponding
// public key is added to the FAAS_INTERNAL_SVC_PUBKEYS env on
// every gatewayd-internal node. Rotation: out of scope for PR-A
// (ADR-120 candidate).
//
// Sealed-at-rest posture (CLAUDE.md G2 §17, round-3 peer-review
// finding #5): the operator MAY instead provision
// FAAS_INTERNAL_SVC_KEY_SEALED_BLOB (the age-encrypted PEM bytes,
// produced by `hostage-gen seal --namespace internal_svc`) and
// FAAS_INTERNAL_SVC_KEY_SEALED_NAMESPACE (default
// "internal_svc"). When the sealed env is present, schedd
// unseals via secretbox.OpenBytesMulti against the host.age
// identities (current + previous) and never touches plaintext
// PEM on disk. The plaintext path stays for local dev + the
// legacy operator who hasn't migrated yet — both paths run
// the same key-shape validation.

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/internalsvc"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// mintState holds the (private, kid) pair the closure reads at
// every call. Stored in an atomic.Pointer so the long-lived
// "cluster_signing_keys_changed" subscriber can swap the key
// without dropping an in-flight synth request.
//
// Rotation scenario (PR-3 + a follow-on ADR-125 rotation
// amendment):
//   - t0: holder is (priv_v1, kid_v1). Every minted JWT carries kid=v1.
//   - t1: operator runs `hostage-gen cluster-rotate` which inserts
//     the new row + retires v1.
//   - t2: trigger fires pg_notify('cluster_signing_keys_changed').
//   - t3: subscriber goroutine re-runs loadClusterInternalSvcKey,
//     parses the new priv, builds a fresh mintState{priv_v2, kid_v2},
//     atomic-swap.
//   - t4: the very next mint uses kid=v2; receivers that still
//     accept v1 (rotation overlap window in the verifier side,
//     PR-3 follow-on) verify both kids cleanly.
//
// The atomic.Pointer is the load-bearing primitive here; without
// it, rotation requires a schedd restart, which is the legacy
// behaviour this PR cluster is replacing.
type mintState struct {
	priv ed25519.PrivateKey
	kid  string
}

// newMintState constructs a fresh mintState from (priv, kid).
// Used by both the initial load path and the rotation path so
// the same validation (key-shape, kid derivation) runs on every
// swap.
func newMintState(priv ed25519.PrivateKey, kid string) *mintState {
	return &mintState{priv: priv, kid: kid}
}

// atomicMinter is the long-lived closure factory backing the
// mintInternalSvcToken signature pkg/sched.loop.go expects. The
// closure reads the atomic.Pointer on every call so rotation
// lands without daemon restart. Returns a small handle struct
// with .Mint + .Rotate so the wiring site (cmd/schedd/main.go)
// can swap the underlying state in the rotation subscriber
// goroutine.
type atomicMinter struct {
	state atomic.Pointer[mintState]
}

// Mint returns a fresh JWT for the given app_id, using whichever
// (priv, kid) is current at the moment of the call.
func (a *atomicMinter) Mint(appID string) (string, error) {
	s := a.state.Load()
	if s == nil {
		return "", errors.New("schedd: minter state is nil (rotation in progress or initial load failed)")
	}
	return internalsvc.Mint("schedd", internalSvcTokenTTL,
		map[string]any{"app_id": appID}, s.priv, s.kid)
}

// Rotate swaps the underlying (priv, kid). Called by the
// cluster_signing_keys_changed subscriber goroutine on every
// delivery. Fail-closed: a rotation that supplies a nil priv is
// a logic bug and is rejected without affecting the current
// state (in-flight mints keep using the previous key until a
// healthy swap lands).
func (a *atomicMinter) Rotate(priv ed25519.PrivateKey, kid string) error {
	if priv == nil || kid == "" {
		return fmt.Errorf("schedd: rotate minter: nil priv or empty kid refused")
	}
	a.state.Store(newMintState(priv, kid))
	return nil
}

// Mint returns the child closure expected by
// sched.ConfigureInternalSvcAuth and sched.WithMintInternalSvcToken.
// Re-exposes the atomicMinter through the same func-type the
// legacy minter did, so the wiring in cmd/schedd/main.go doesn't
// change between cluster-key and per-host-key paths.
func (a *atomicMinter) AsFunc() func(string) (string, error) {
	return a.Mint
}

const (
	// internalSvcTokenTTL is the JWT exp claim window (ADR-119
	// plan: short TTL for replay-attack posture, ≤30s). Chosen
	// at 30s to match a typical cron-fire latency budget
	// (gatewayd-internal wake + first byte ≤25s in p99).
	internalSvcTokenTTL = 30 * time.Second
	// internalSvcKeyPathEnv is the env var that overrides the
	// default keypair path.
	internalSvcKeyPathEnv = "FAAS_INTERNAL_SVC_KEY_PATH"
	// internalSvcKeySealedEnv holds the age-encrypted PEM bytes
	// (output of `hostage-gen seal --namespace internal_svc`).
	// Round-3 G2 §17 closure: secrets at rest are sealed via
	// host.age. Operators set this in the systemd unit
	// EnvironmentFile; schedd unseals via host.age on boot.
	internalSvcKeySealedEnv = "FAAS_INTERNAL_SVC_KEY_SEALED_BLOB"
	// internalSvcKeySealedNamespaceEnv overrides the seal
	// namespace; defaults to "internal_svc" when unset.
	internalSvcKeySealedNamespaceEnv = "FAAS_INTERNAL_SVC_KEY_SEALED_NAMESPACE"
	// internalSvcKeySealedNamespaceDefault matches the
	// reservation in ADR-119 §Deployment requirements
	// ("namespace 'internal_svc' under host.age").
	internalSvcKeySealedNamespaceDefault = "internal_svc"
	// defaultInternalSvcKeyPath is the production path the
	// operator is expected to provision. Used when the env is
	// unset.
	defaultInternalSvcKeyPath = "/etc/faas/secrets/internal-svc/schedd.ed25519"
)

// newSchedInternalSvcMinter loads the schedd Ed25519 keypair
// from the cluster_signing_keys PG row (PR-3 / ADR-125
// fleet-wide signing key), falling back to FAAS_INTERNAL_SVC_KEY_PATH
// (or its sealed sibling) when the cluster row is missing or
// not unsealable on this box. Returns an *atomicMinter that
// callers can both use as the func-type the schedd engine
// expects (call .AsFunc()) and feed into the rotation
// subscriber (call .Rotate(priv, kid) on every delivery).
//
// Fallback chain (in order):
//
//  1. cluster_signing_keys row (PR-3 / ADR-125) — unsealed
//     via host.age identities on this box. The cluster row
//     is the multi-host source of truth; every schedd in
//     the fleet mints with the same kid.
//  2. FAAS_INTERNAL_SVC_KEY_SEALED_BLOB — sealed-at-rest
//     single-box dev + legacy operator-migration path.
//  3. FAAS_INTERNAL_SVC_KEY_PATH (or default path) —
//     plaintext PEM, generated-on-missing.
//
// Sealed-at-rest mode (step 2): if
// FAAS_INTERNAL_SVC_KEY_SEALED_BLOB is set, the plaintext-PEM
// path is skipped entirely and the unsealed bytes are used.
// The host.age identities are loaded via
// secretbox.LoadHostKeys(secretbox.DefaultHostKeyDir) — current
// first, previous second — so a rotation overlap window is
// supported without daemon restart.
func newSchedInternalSvcMinter(ctx context.Context, store *state.PgStore, log *slog.Logger) (*atomicMinter, error) {
	if log == nil {
		log = slog.Default()
	}
	m := &atomicMinter{}
	// Step 1: cluster-wide PG key (PR-3 / ADR-125). Most
	// production schedds hit this path; the per-host fallback
	// chain below is the operator-migration window.
	if store != nil {
		priv, kid, err := loadClusterInternalSvcKey(ctx, store, log)
		if err == nil {
			if rErr := m.Rotate(priv, kid); rErr != nil {
				return nil, fmt.Errorf("schedd: prime atomicMinter with cluster key: %w", rErr)
			}
			log.Info("schedd: internal-svc minter loaded",
				"svc_name", "schedd",
				"kid", kid,
				"source", "cluster_signing_keys",
				"ttl", internalSvcTokenTTL.String())
			return m, nil
		}
		if !errors.Is(err, ErrClusterKeyUnavailable) {
			// Hard error — log + bail. The fallback chain is
			// for "row missing or unseal failed", not for
			// "PG is unreachable and pgx is erroring".
			return nil, fmt.Errorf("schedd: cluster key load: %w", err)
		}
		log.Info("schedd: cluster_signing_keys unavailable; falling back to per-host key path",
			"reason", err.Error())
	}
	priv, source, err := loadSchedInternalSvcKey(log)
	if err != nil {
		return nil, err
	}
	pub := priv.Public().(ed25519.PublicKey)
	kid := kidFromPub(pub)
	if rErr := m.Rotate(priv, kid); rErr != nil {
		return nil, fmt.Errorf("schedd: prime atomicMinter with per-host key: %w", rErr)
	}
	log.Info("schedd: internal-svc minter loaded",
		"svc_name", "schedd",
		"kid", kid,
		"source", source,
		"ttl", internalSvcTokenTTL.String())
	return m, nil
}

// mintClosure is the small closure factory shared by the cluster
// and per-host paths so the future-call surface (one JWT per
// app_id) lives in exactly one place. PR-3 lifts it from
// newSchedInternalSvcMinter so both paths produce the same
// closure shape.
func mintClosure(svcName string, priv ed25519.PrivateKey, kid string) func(string) (string, error) {
	return func(appID string) (string, error) {
		claims := map[string]any{
			// Future: per-app key-pinning — the receiver
			// could refuse tokens whose app_id claim doesn't
			// match the routed app. Today's receiver just
			// checks svcName + aud + exp + sig, so we include
			// app_id for audit-log fidelity only.
			"app_id": appID,
		}
		return internalsvc.Mint(svcName, internalSvcTokenTTL, claims, priv, kid)
	}
}

// loadSchedInternalSvcKey is the new top-level loader (round-3
// G2 §17 closure). It picks the sealed-at-rest path when
// FAAS_INTERNAL_SVC_KEY_SEALED_BLOB is set, falling back to the
// plaintext-PEM path otherwise. Returns the private key + a
// short source tag for the boot log ("sealed" vs
// "plaintext_pem:<path>").
func loadSchedInternalSvcKey(log *slog.Logger) (ed25519.PrivateKey, string, error) {
	if log == nil {
		// Same default as newSchedInternalSvcMinter — nil-safe
		// for tests that bypass the public entry point.
		log = slog.Default()
	}
	if sealed := os.Getenv(internalSvcKeySealedEnv); sealed != "" {
		priv, err := loadSchedKeySealed(sealed, log)
		if err != nil {
			return nil, "", err
		}
		return priv, "sealed", nil
	}
	keyPath := os.Getenv(internalSvcKeyPathEnv)
	if keyPath == "" {
		keyPath = defaultInternalSvcKeyPath
	}
	priv, err := loadOrGenerateSchedKey(keyPath, log)
	if err != nil {
		return nil, "", err
	}
	return priv, "plaintext_pem:" + keyPath, nil
}

// loadSchedKeySealed unseals the PEM bytes from
// FAAS_INTERNAL_SVC_KEY_SEALED_BLOB against the host.age
// identities (current + previous). Round-3 G2 §17 closure —
// the key never sits in plaintext on disk. The seal namespace
// is taken from FAAS_INTERNAL_SVC_KEY_SEALED_NAMESPACE
// (default "internal_svc"); the namespace is checked on open
// so a stolen ciphertext from a different namespace cannot be
// replayed against the internal-svc path.
func loadSchedKeySealed(sealedB64 string, log *slog.Logger) (ed25519.PrivateKey, error) {
	identities, err := secretbox.LoadHostKeys(filepath.Dir(secretbox.DefaultHostKeyPath))
	if err != nil {
		return nil, fmt.Errorf("schedd: load host.age identities for sealed key: %w", err)
	}
	if len(identities) == 0 {
		return nil, errors.New("schedd: no host.age identities available; cannot unseal FAAS_INTERNAL_SVC_KEY_SEALED_BLOB")
	}
	ns := os.Getenv(internalSvcKeySealedNamespaceEnv)
	if ns == "" {
		ns = internalSvcKeySealedNamespaceDefault
	}
	// Decode the base64 wrapper around the age ciphertext
	// (matches the on-wire shape produced by `hostage-gen
	// seal`, which base64-encodes the age binary output so
	// it lands cleanly in EnvironmentFile / systemd
	// credentials). If the operator pastes raw age output
	// instead, fall through to OpenBytesMulti on the raw
	// bytes — both shapes are accepted to keep migration
	// friction low.
	var sealed []byte
	if raw, decErr := base64.StdEncoding.DecodeString(sealedB64); decErr == nil && looksLikeAgeBlob(raw) {
		sealed = raw
	} else {
		sealed = []byte(sealedB64)
	}
	gotNS, plaintext, err := secretbox.OpenBytesMulti(identities, sealed)
	if err != nil {
		return nil, fmt.Errorf("schedd: unseal FAAS_INTERNAL_SVC_KEY_SEALED_BLOB: %w", err)
	}
	if gotNS != ns {
		return nil, fmt.Errorf("schedd: sealed key namespace=%q, want %q (refusing cross-namespace replay)", gotNS, ns)
	}
	priv, err := parseSchedKeyPEM(plaintext)
	if err != nil {
		return nil, fmt.Errorf("schedd: parse unsealed PEM: %w", err)
	}
	log.Info("schedd: unsealed internal-svc key from host.age",
		"namespace", gotNS, "identities", len(identities))
	return priv, nil
}

// looksLikeAgeBlob is a cheap heuristic: age output starts
// with the ASCII armor header "-----BEGIN AGE ENCRYPTED FILE-----".
// Used to decide whether the env value is base64-wrapped or
// raw. False positives are harmless (OpenBytesMulti fails
// loudly); false negatives fall through to the raw-bytes path
// and OpenBytesMulti there.
func looksLikeAgeBlob(b []byte) bool {
	const prefix = "-----BEGIN AGE ENCRYPTED FILE-----"
	return len(b) >= len(prefix) && string(b[:len(prefix)]) == prefix
}

// parseSchedKeyPEM decodes the unsealed bytes as the same
// PEM/PKCS#8 Ed25519 shape loadOrGenerateSchedKey writes.
// Lifted so the sealed path produces the same validated
// ed25519.PrivateKey regardless of where the bytes came from.
func parseSchedKeyPEM(data []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("schedd: sealed payload is not PEM-encoded")
	}
	if block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("schedd: sealed payload has unexpected PEM block type %q", block.Type)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("schedd: parse PKCS#8: %w", err)
	}
	priv, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("schedd: sealed payload is not an Ed25519 key")
	}
	return priv, nil
}

// loadOrGenerateSchedKey loads the Ed25519 private key from
// the given PEM file path, or generates a fresh keypair and
// persists it if the file is missing. The PEM shape is the
// PKCS#8 PrivateKey wrapped in "PRIVATE KEY".
//
// Round-3 follow-up note: this plaintext-PEM path is the
// dev-friendly default. Operators are nudged toward the sealed
// path (FAAS_INTERNAL_SVC_KEY_SEALED_BLOB) — the WARN emitted
// when a fresh keypair is generated now also includes the
// `hostage-gen seal --namespace internal_svc` recipe so the
// migration is one command away.
func loadOrGenerateSchedKey(keyPath string, log *slog.Logger) (ed25519.PrivateKey, error) {
	if data, err := os.ReadFile(keyPath); err == nil {
		return parseSchedKeyPEM(data)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("schedd: read %s: %w", keyPath, err)
	}
	// File missing — generate a fresh keypair and persist.
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, fmt.Errorf("schedd: generate keypair: %w", err)
	}
	if dir := filepath.Dir(keyPath); dir != "" {
		if mkErr := os.MkdirAll(dir, 0700); mkErr != nil && !errors.Is(mkErr, os.ErrExist) {
			log.Warn("schedd: mkdir for internal-svc key failed; minter will not persist",
				"path", dir, "err", mkErr.Error())
			return priv, nil
		}
	}
	marshalled, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		log.Warn("schedd: marshal internal-svc key failed; minter will not persist",
			"err", err.Error())
		return priv, nil
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: marshalled})
	if wErr := os.WriteFile(keyPath, pemBytes, 0600); wErr != nil {
		log.Warn("schedd: persist internal-svc key failed; minter will be in-memory only",
			"path", keyPath, "err", wErr.Error())
		return priv, nil
	}
	log.Warn("schedd: generated a fresh internal-svc keypair — seal it via 'hostage-gen seal --namespace internal_svc < "+keyPath+"' and set FAAS_INTERNAL_SVC_KEY_SEALED_BLOB",
		"path", keyPath)
	return priv, nil
}

// kidFromPub delegates to internalsvc.KidFromPub — the
// canonical kid derivation. Round-3 peer-review #7 (kid
// format divergence): this used to be a local helper that
// produced base64-of-[:16] while pkg/internalsvc's auto-derive
// produced hex-of-[:8]. The drift made diagnostic logs that
// key off kid unreliable. Now both surfaces call
// internalsvc.KidFromPub — a single source of truth. The
// local kidFromPub is kept as a thin wrapper so the boot log
// line and any future code path don't have to import the
// internalsvc package-level function explicitly.
func kidFromPub(pub ed25519.PublicKey) string {
	return internalsvc.KidFromPub(pub)
}
