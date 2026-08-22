// vmmd self-registration (issue #98 / ADR-028).
//
// On startup, every vmmd Upserts its own row into compute_nodes so
// schedd's placement engine can route wakes to it without an
// operator-driven POST /v1/compute-nodes. UPSERT semantics (rather
// than plain INSERT) means a rebooting box comes back with the same
// UUID + created_at as before — schedd caches the id in memory and
// loses no state across the restart. ON CONFLICT re-applies the
// operator's resource numbers from vmmd.toml AND re-activates a
// previously drained row (active=true), so a fix-up after a network
// blip doesn't need an admin click.
//
// The full node registration happens before the gRPC listener binds:
// if the upsert fails (Postgres down, schema drift), vmmd exits
// rather than serving traffic with no identity. That fail-closed
// stance matches the host-key load above it (the daemon refuses to
// start without its unseal key for the same reason).
//
// Multi-host safety cluster PR-4 (audit F6 / ADR-052 amendment):
// the upsert also stamps the local leaf cert's fingerprint
// (pkg/pki.LoadCertificateFingerprint) on the row's
// cert_fingerprint column. On a conflict, the existing row's
// fingerprint is compared against the freshly-computed local one;
// if they differ, the upsert fails-closed with
// state.ErrCertFingerprintDrift. This guards against a leaked cert
// being silently replaced by an attacker who issued a new leaf
// under the same CA — the existing fingerprint column is the
// public-key-pinning attestation, and refusing to overwrite it on
// mismatch keeps the operator's window of detection open.
//
// The `default-local` row seeded by migration 00024 has the same
// name the vmmd uses when [compute_node].name is left at its
// default — short hostname — only when hostname equals
// "default-local" (rare; tests/legacy). Production operators set
// [compute_node].name explicitly to avoid colliding with the seed
// row, and the vmmd config default-empts that collision by leaving
// NodeName empty when no override is set (skip self-registration
// entirely; schedd only knows this node via default-local).

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/pki"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// registerComputeNode wires the daemon's startup upsert. Returns
// the registered node on success (which the caller logs and uses to
// confirm the placement engine sees the box) or an error that the
// caller surfaces as a fatal startup failure.
//
// detectOverlayIP is function-typed so tests can inject a stub that
// returns "100.64.0.1" without shelling out to `tailscale ip -4`
// (Linux/macOS only — gated to //go:build metal otherwise).
//
// targetURL is the dial target schedd/gatewayd use to reach this
// vmmd (the value written to compute_nodes.target_url). It is
// intentionally separate from the gRPC bind target — a bind like
// `tcp://0.0.0.0:50051` is fine for listening on all interfaces
// but is NOT a routable dial target. The caller passes the dial
// target here; registerComputeNode does not infer it from the
// bind address (the conflation between the two was the load-
// bearing bug fixed in the second-box cutover PR; see
// docs/runbooks/multi-host-rollout.md §3.5).
func registerComputeNode(ctx context.Context, st state.Store, cfg ComputeNodeConfig, targetURL string, detectOverlayIP func(context.Context) (string, error), log *slog.Logger, scheddTargets ...string) (state.ComputeNode, error) {
	name := strings.TrimSpace(cfg.NodeName)
	if name == "" {
		// Empty name = operator chose not to self-register. vmmd
		// still serves traffic; schedd only routes via default-local
		// (migration 00024) so the gateway's per-node client cache
		// only ever holds that one entry. This is the dev-mode
		// path; multi-node boxes must set [compute_node].name.
		log.Info("vmmd: skipping self-registration ([compute_node].name empty); default-local only")
		return state.ComputeNode{}, nil
	}

	// Issue #900: the TLS CN verifier
	// (pkg/wire/node_verifier.go::LookupCN) only accepts CNs that
	// appear in the registered-set snapshot, which is populated
	// from compute_nodes.name. The canonical CNs in the registered
	// set are `*.faas` (vmmd.faas, schedd.faas, ...) — the verifier
	// is a whitelist. Without a `.faas` suffix on the
	// operator-supplied [compute_node].name, the row's name column
	// never matches a registered CN, the handshake aborts at
	// the verifier with ErrNodeVerifierCNMismatch, and schedd
	// drops the capacity report with ErrEmptySignature
	// (pkg/sched/capacity.go:99).
	//
	// Operator-friendly resolution: append `.faas` to the
	// computed name when missing. The rewrite is local to the
	// upsert row — cfg.NodeName is left untouched so re-reading
	// the config shows the operator's original intent. The
	// success log line at the bottom of this function logs
	// got.Name (the rewritten form), so the operator sees the
	// actual value in compute_nodes.name.
	//
	// NOT addressed here: the TLS leaf cert path. The leaf is
	// generated by `gregale pki init` using the operator's TOML
	// value as the CN. If the leaf's CN does not match the
	// rewritten row name, the verifier still rejects. A future
	// PR (cmd/gregalectl/pki_init.go) will rewrite the leaf CN
	// at issue time. This PR fixes the registered-set side; the
	// leaf-cert side is a follow-up.
	const faasSuffix = ".faas"
	if !strings.HasSuffix(name, faasSuffix) {
		log.Info("vmmd: appending .faas suffix to compute_node name",
			"original", name, "rewritten", name+faasSuffix)
		name = name + faasSuffix
	}

	if cfg.VPCPUs <= 0 || cfg.MemMB <= 0 || cfg.MaxConcurrency <= 0 || cfg.AdmissionCeilingMB <= 0 {
		return state.ComputeNode{}, fmt.Errorf("vmmd: [compute_node] fields must be > 0 (got vpcpus=%d mem_mb=%d max_concurrency=%d admission_ceiling_mb=%d)",
			cfg.VPCPUs, cfg.MemMB, cfg.MaxConcurrency, cfg.AdmissionCeilingMB)
	}
	// Issue #938 / PR-A: VCPUBudget is treated specially because the
	// struct literal default in config.go is already the canonical
	// api.VCPUSlots (160) — operators who omit it get a sensible single-
	// box value. Negative values are still rejected (the SQL CHECK
	// constraint is > 0, and a negative would either default to 160 or
	// fail the upsert depending on the fallback order). Zero is
	// explicitly permitted here so the api.VCPUSlots fallback below
	// remains the single source of truth for the "no override" path.
	//
	// Asymmetry with cmd/vmmd/config.go (review finding #4 on PR #940):
	// LoadConfig rejects vcpu_budget <= 0 because TOML `vcpu_budget = 0`
	// is an explicit operator mistake. This layer accepts 0 and falls
	// back to api.VCPUSlots because the test seam
	// (register_test.go:TestRegisterComputeNode_DefaultsVCPUBudgetFromAPI)
	// calls registerComputeNode directly with the struct-default zero
	// value to pin the "operator omitted the field" fallback. In
	// production, LoadConfig has already rejected a zero before this
	// code runs, so the fallback only fires for the test seam and
	// for any future caller that bypasses LoadConfig (none today).
	// Do not unify the two layers without updating the test.
	if cfg.VCPUBudget < 0 {
		return state.ComputeNode{}, fmt.Errorf("vmmd: [compute_node].vcpu_budget must be >= 0 (got %d)", cfg.VCPUBudget)
	}

	overlayIP := strings.TrimSpace(cfg.OverlayIP)
	if overlayIP == "" && detectOverlayIP != nil {
		ip, err := detectOverlayIP(ctx)
		if err != nil {
			// Best-effort: an empty overlay_ip is fine for
			// default-local (which dials over the unix socket,
			// never over an overlay IP). We log and continue
			// rather than fail-closed because remote-node
			// routing requires overlay_ip, but a missing
			// tailscale binary in a dev box is not a vmmd
			// startup error.
			log.Warn("vmmd: overlay IP detection failed; continuing", "err", err.Error())
		} else {
			overlayIP = ip
		}
	}

	// PR-4 (multi-host safety audit F6 / ADR-052 amendment):
	// compute the cert fingerprint from the local leaf so the
	// upsert can refuse a drift on conflict. The fingerprint is
	// advisory — vmmd refusing to start because the cert file is
	// missing or unreadable is too loud for the dev path
	// (`gregale pki init` may not have run yet on a brand-new box
	// that hasn't yet opened a TLS listener — that box has no
	// cert to fingerprint). We log a warning and let the upsert
	// proceed with a nil fingerprint, which leaves the row's
	// existing cert_fingerprint intact via the COALESCE.
	//
	// Production boxes run `gregale pki init` before vmmd starts,
	// so the leaf always exists. The fallback (no fingerprint
	// stamped) is a soft-no-op that the operator can detect via
	// `gregale doctor` (PR-4 follow-on; out of scope here).
	var certFP *string
	if certPath, ok := vmmdServerCertPath(); ok {
		fp, err := pki.LoadCertificateFingerprint(certPath)
		if err != nil {
			// Loud-but-non-fatal: missing cert is the
			// default-local / dev-box path (no `gregale pki
			// init` yet); a malformed cert is a real
			// problem but the perms gate at pki.go will
			// have already produced a clear error message.
			log.Warn("vmmd: cert fingerprint load skipped", "path", certPath, "err", err.Error())
		} else {
			certFP = &fp
		}
	}

	row := state.ComputeNode{
		Name:               name,
		TargetURL:          targetURL,
		VPCPUs:             cfg.VPCPUs,
		MemMB:              cfg.MemMB,
		MaxConcurrency:     cfg.MaxConcurrency,
		AdmissionCeilingMB: cfg.AdmissionCeilingMB,
		VCPUBudget:         cfg.VCPUBudget,
		Active:             true,
		CertFingerprint:    certFP,
	}
	if len(scheddTargets) > 0 && strings.TrimSpace(scheddTargets[0]) != "" {
		scheddTarget := strings.TrimSpace(scheddTargets[0])
		row.ScheddTargetURL = &scheddTarget
	}
	// Issue #938 / PR-A: the migration 00123 CHECK constraint
	// (vcpu_budget > 0) rejects 0, and the struct-default path leaves
	// the field at 0 unless the operator set it. Fall back to
	// api.VCPUSlots (160, the migration backfill default) so single-box
	// dev never trips the CHECK. The < 0 guard above ensures this
	// fallback only fires for the "operator omitted" path, not the
	// "operator wrote a negative" path.
	if row.VCPUBudget <= 0 {
		row.VCPUBudget = api.VCPUSlots
	}
	got, err := st.UpsertComputeNodeFromVmmd(ctx, row)
	if err != nil {
		return state.ComputeNode{}, fmt.Errorf("vmmd: upsert compute_nodes %q: %w", name, err)
	}
	log.Info("vmmd: compute_node registered",
		"name", got.Name, "id", got.ID,
		"target_url", got.TargetURL,
		"vpcpus", got.VPCPUs, "mem_mb", got.MemMB,
		"admission_ceiling_mb", got.AdmissionCeilingMB,
		"vcpu_budget", got.VCPUBudget)
	_ = overlayIP // reserved: pkg/state.ComputeNode will get OverlayIP in the migration-00026 follow-up.
	return got, nil
}

// defaultDetectOverlayIP runs `tailscale ip -4` and prefers the IP
// that lives in cfg.OverlayCIDR (falling back to
// api.DefaultOverlayCIDR() when the field is unset).
//
// Returns ("", nil) when tailscale isn't installed (single-box dev)
// or when no overlay IP is configured (WireGuard-mode operators
// set [compute_node].overlay_ip explicitly). Returns ("", err) on
// an actual exec failure that isn't "binary missing" — e.g.
// tailscale installed but the daemon is down — so the caller can
// log the failure rather than silently proceeding.
//
// Mega-PR-B Commit 3: the v1 first-line behavior is preserved as
// the fall-through path when no candidate matches PreferCIDR. The
// scoring logic (parseTailscaleIPLines, scoreByCIDR) lives in
// overlay_detect.go so it's unit-testable without shelling out.
func defaultDetectOverlayIP(ctx context.Context, cfg ComputeNodeConfig) (string, error) {
	prefer := api.DefaultOverlayCIDR()
	if cidr := strings.TrimSpace(cfg.OverlayCIDR); cidr != "" {
		p, err := netip.ParsePrefix(cidr)
		if err != nil {
			return "", fmt.Errorf("compute_node.overlay_cidr %q: %w", cidr, err)
		}
		prefer = p
	}
	// PR scale-out tier-1 residual (Gap #5): forward the
	// operator-pinned NIC into the detector. Empty (the v1
	// default) keeps the PreferCIDR scoring path unchanged. When
	// set, the detector pins to that interface's IPv4 address and
	// only falls back to PreferCIDR scoring when the pinned NIC
	// is missing or has no IPv4 address — the "never silently
	// fail" posture that preserves the v1 contract.
	return detectOverlayIP(ctx, OverlayDetector{
		PreferCIDR:      prefer,
		PinnedInterface: strings.TrimSpace(cfg.OverlayInterface),
	})
}

// registerComputeNodeKey writes the public half of vmmd's signing
// key into compute_node_keys (migration 00076 / ADR-053) so
// schedd's NodeKeyRegistry can verify node_signature on every
// CapacityReport.
//
// The row is keyed by (compute_node_id, key_id); both vmmd and
// schedd compute key_id the same way (SHA-256(SPKI) hex, lowercase)
// via pkg/sched.KeyIDForPublicKey. ON CONFLICT DO NOTHING means a
// re-register (vmmd restart, key rotation mid-flight) is
// idempotent — a future runbook (issue #316) will own the rotation
// ceremony; this function is the per-startup no-op-on-repeat.
//
// nil nodeKey is the pre-slice-3 mode (no node.key on disk; legacy
// schedd accepts unsigned reports). The function returns nil
// without writing so a vmmd boot that hasn't yet received a signing
// key from the bootstrap step doesn't fail-closed against a
// condition that is, by design, supposed to be a soft no-op on
// the wire. The pre-slice-3 schedd already accepts an empty
// node_signature field (ADR-016 additive wire), so this is not a
// silent semantic change.
//
// The PEM body is a SubjectPublicKeyInfo (RFC 7468 §13) wrapped in
// a PUBLIC KEY block — the same shape the schedd-side
// parsePublicKeyPEM accepts in pkg/sched/nodekeys.go. Reusing the
// PEM wire rather than passing raw DER keeps the migration 00076
// schema (`public_key_pem text`) unchanged and gives an operator
// reading the table a copy-paste-able verification artifact.
//
// Fail-closed: if the upsert fails (Postgres down, schema drift),
// the function surfaces the error so vmmd exits rather than
// serving unsigned reports against a registry that won't accept
// them. The capacity publisher below this block in main.go will
// already be wired with the signing key — a successful start but a
// missing compute_node_keys row is exactly the silent-degrade case
// the F7 counter (`capacity_signature_rejected_total`) is supposed
// to surface, and failing here turns the counter into a tripwire
// instead of a silent signal.
func registerComputeNodeKey(ctx context.Context, st state.Store, nodeID string, nodeKey *ecdsa.PrivateKey, nodeKeyID string, log *slog.Logger) error {
	if nodeKey == nil {
		// Pre-slice-3 mode. Log so an operator looking at the
		// startup sequence can correlate this with the
		// "capacity reports unsigned" line emitted at the
		// call site.
		log.Info("vmmd: skipping compute_node_keys upsert (no node.key); pre-slice-3 mode")
		return nil
	}
	if nodeID == "" {
		// registerComputeNode was called with an empty name,
		// so there's no compute_nodes.id to attach the key
		// to. Match that path's silent-skip posture: legacy
		// single-box dev never writes a row, never serves a
		// signed report.
		log.Info("vmmd: skipping compute_node_keys upsert (no compute_nodes.id; default-local only)")
		return nil
	}

	pemBytes, err := publicKeyPEM(&nodeKey.PublicKey)
	if err != nil {
		return fmt.Errorf("vmmd: marshal node public key: %w", err)
	}

	if err := st.UpsertNodeKey(ctx, nodeID, nodeKeyID, string(pemBytes)); err != nil {
		return fmt.Errorf("vmmd: upsert compute_node_keys (node=%q, key_id=%s): %w", nodeID, nodeKeyID, err)
	}
	log.Info("vmmd: compute_node_keys row upserted",
		"node_id", nodeID, "key_id", nodeKeyID)
	return nil
}

// publicKeyPEM encodes an ECDSA public key as a SubjectPublicKeyInfo
// PEM block. The block type is "PUBLIC KEY" (RFC 7468 §13), the
// only type parsePublicKeyPEM accepts; DER is the PKIX form
// (RFC 5480) produced by x509.MarshalPKIXPublicKey.
//
// Centralised so registerComputeNodeKey and the test helper both
// call the same marshaller — and so a future curve change (ADR-053
// permits none today; the F4 test pins P-256) has exactly one site
// to update.
func publicKeyPEM(pub *ecdsa.PublicKey) ([]byte, error) {
	if pub == nil {
		return nil, errors.New("nil public key")
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("marshal PKIX: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), nil
}

// Compile-time guard: registerComputeNodeKey depends on
// sched.KeyIDForPublicKey existing; pin it via an unused-import
// reference so a future refactor that drops the helper surfaces
// at build time rather than silently producing an empty key_id.
var _ = sched.KeyIDForPublicKey

// vmmdServerCertPath returns the on-disk path to the vmmd server
// leaf cert (the cert that vmmd presents to inbound clients —
// schedd, meterd, gatewayd-internal — for mTLS). The (false, "")
// return value signals "no cert path known" (e.g. a dev box
// running vmmd without `gregale pki init`), which lets the caller
// fall through with a nil fingerprint rather than fail-closed.
//
// The constant path matches pkg/pki.Roles()'s vmmd/server entry
// (CommonName "vmmd.faas", Directory "vmmd", Filename "server"):
// /etc/faas/tls/vmmd/server.crt. Overridable via
// FAAS_TLS_DIR for tests + non-standard installs; the env-var
// lookup mirrors pkg/pki.DefaultRootDir's posture (constant in
// production, overridable in test).
func vmmdServerCertPath() (string, bool) {
	root := pki.DefaultRootDir
	if v := strings.TrimSpace(envOrDefault("FAAS_TLS_DIR", "")); v != "" {
		root = v
	}
	path := root + "/vmmd/server.crt"
	// Stat is best-effort: a missing file at this path is the
	// default-local / dev-box path; vmmd still serves traffic
	// over the unix socket without mTLS in that mode. Returning
	// (path, true) so the caller attempts the load (and gets the
	// os.ErrNotExist error, which is wrapped at the call site as
	// a "cert fingerprint load skipped" warning).
	return path, true
}

// envOrDefault is the small helper that reads an env var with a
// fallback. Mirrors the project-wide pattern in cmd/vmmd/config.go
// — kept inline here to avoid pulling config.go's surface into
// the register_test.go fixture set. Tests use t.Setenv for the
// FAAS_TLS_DIR override (no test seam required — os.Getenv reads
// through t.Setenv's mutation).
func envOrDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
