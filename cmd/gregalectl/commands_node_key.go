// commands_node_key.go — operator-side CLI for the per-node
// CapacityReport signing keypair (ADR-053 / Tier 1 Phase 2). The
// private key lives at /etc/faas/secrets/vmmd/node.key (mode 0400
// root:root, PKCS#8 ECDSA P-256) and is loaded by vmmd at startup
// via cmd/vmmd/main.go::loadNodeSigningKey. The public half is
// published to schedd's compute_node_keys table by vmmd's
// registerComputeNodeKey UPSERT on the same startup; the matching
// key_id (SHA-256 hex of the SPKI) is computed identically by both
// sides via pkg/sched.KeyIDForPublicKey.
//
// This is the OPERATOR surface, not the customer surface: there is
// no authedClient() call, no SDK, no API call — every leaf is a
// local file-system operation against the canonical
// /etc/faas/secrets/vmmd/ paths (or the caller-supplied
// --node-key / --node-key-pub flags). It is the load-bearing
// companion to docs/runbooks/multi-host-rollout.md §3 ("Bootstrap:
// node signing key") so an operator can run a single
// `gregalectl node-key init` instead of hand-rolling openssl + chown.
//
// The namespace `gregale keys` is already taken by the customer-
// facing API-key manager (commands2.go::cmdKeys); the operator-side
// cosign keypair CLI is `gregalectl sign-keys` (commands_sign_keys.go);
// the per-node slice-3 signing key lives in its own namespace
// `gregalectl node-key` for symmetry. Three leaves:
//   - init   — write a fresh keypair (refuses overwrite)
//   - rotate — write a fresh keypair with --force
//   - status — print mode + fingerprint + paths for both files
//
// Force defaults mirror sign-keys:
//   - init   defaults force=false (refuse overwrite)
//   - rotate defaults force=true  (bare rotate MUST overwrite)
//
// The asymmetry is load-bearing (TestSignKeyFlagDefaults pins the
// sign-keys analogue; the same shape applies here).
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/sched"
)

// canonicalNodeKeyPath is the canonical install location for the
// slice-3 node signing key. Mirrors cmd/vmmd/main.go:67 — the two
// constants MUST stay in sync or vmmd will fail to load the key the
// operator just generated.
//
// Mode is 0400 root:root — vmmd is the only root daemon (per
// CLAUDE.md §11), so the file is owner-only. group/other readable
// is an unambiguous PKI tamper signal; vmmd's loadNodeSigningKey
// refuses to start (errNodeKeyInsecure) in that posture.
const canonicalNodeKeyPath = "/etc/faas/secrets/vmmd/node.key"

// canonicalNodeKeyPubPath is the operator's verification artifact
// for the slice-3 signing key. vmmd does not consume this file —
// registerComputeNodeKey derives the public PEM from the loaded
// private key itself — but the file is written here for symmetry
// with sign-keys (sign-pub.pem) so an operator can copy/paste the
// SPKI into another tool without re-extracting it.
const canonicalNodeKeyPubPath = "/etc/faas/secrets/vmmd/node.pub"

const dispatchNodeKey = "node-key"

const (
	subNodeInit   = "init"
	subNodeRotate = "rotate"
	subNodeStatus = "status"
)

// cmdNodeKey is the parent dispatcher. Same contract as
// cmdSignKeys: zero args prints usage; init/rotate/status fans to
// the matching helper; unknown subcommands return 1 with a usage
// hint.
func cmdNodeKey(args []string) int {
	parent, _ := lookupCliCommand("node-key")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregalectl node-key <init|rotate|status> [flags]", "node-key")
		return 1
	}
	switch args[0] {
	case subNodeInit:
		return cmdNodeKeyInit(args[1:])
	case subNodeRotate:
		return cmdNodeKeyRotate(args[1:])
	case subNodeStatus:
		return cmdNodeKeyStatus(args[1:])
	default:
		sug, _ := suggestSubcommand(args[0], parent)
		fmt.Fprintf(os.Stderr, "gregalectl node-key: unknown subcommand %q (known: init, rotate, status)\n", args[0])
		maybeSuggestSub(sug)
		return 1
	}
}

// nodeKeyFlags is the flag surface shared by init/rotate/status.
// Status doesn't use --force but parsing it as a no-op is harmless
// (ContinueOnError + NArg check catches any positional drift).
type nodeKeyFlags struct {
	nodeKey string
	nodePub string
	force   bool
}

func newNodeKeyFlags(name string, defaultForce bool) (*flag.FlagSet, *nodeKeyFlags) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	f := &nodeKeyFlags{}
	fs.StringVar(&f.nodeKey, "node-key", canonicalNodeKeyPath,
		"path to the node signing private key (mode 0400 root:root on the canonical install)")
	fs.StringVar(&f.nodePub, "node-key-pub", canonicalNodeKeyPubPath,
		"path to the node signing public key (mode 0444, world-readable; operator verification artifact)")
	fs.BoolVar(&f.force, "force", defaultForce,
		"overwrite an existing keypair (rotate only)")
	return fs, f
}

// writeNodeKeyPair is the shared write path. Mirrors writeKeyPair
// for cosign; the only structural difference is the on-disk mode
// (0400 instead of 0440) and the absence of a group-chown step
// (vmmd is the only root daemon). Errors carry the operator-facing
// hint so an operator running this from the multi-host rollout
// runbook gets a useful message when the bootstrap user is wrong.
func writeNodeKeyPair(force bool, privPath, pubPath string) error {
	privPEM, pubPEM, err := generateNodeKeyPEM()
	if err != nil {
		return fmt.Errorf("generate node keypair: %w", err)
	}
	if err := writeNodeKeyFiles(privPath, pubPath, privPEM, pubPEM, force); err != nil {
		return err
	}
	return nil
}

// generateNodeKeyPEM mints a fresh ECDSA P-256 keypair (per
// ADR-053 §3 — fixed curve; the wire's 64-byte raw (r||s) signature
// shape assumes 32-byte r and 32-byte s). Returns PEM-encoded
// PKCS#8 PRIVATE KEY + PKIX PUBLIC KEY blocks — the same shapes
// cmd/vmmd/main.go::loadNodeSigningKey and
// pkg/sched/nodekeys.go::parsePublicKeyPEM accept.
//
// Mirrors pkg/cosign/generate.go::GenerateKeyPair rather than
// importing it directly: cosign's writer is paired with
// WriteKeyPairForGroup (0440 root:gregale); the node-key writer
// needs 0400 root:root + a separate .pub artifact. A small inline
// pair avoids dragging cosign's group-chown path into the slice-3
// bootstrap (the surface is small enough to be obvious from one
// read).
func generateNodeKeyPEM() (privPEM, pubPEM []byte, err error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("ecdsa.GenerateKey(P-256): %w", err)
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("x509.MarshalPKCS8PrivateKey: %w", err)
	}
	privPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER})
	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("x509.MarshalPKIXPublicKey: %w", err)
	}
	pubPEM = pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
	return privPEM, pubPEM, nil
}

// writeNodeKeyFiles writes the private key at 0400 and the public
// at 0444, refusing to overwrite unless force=true. The
// os.WriteFile call is preceded by an os.Stat so the existence
// check is the load-bearing refusal point (write itself doesn't
// distinguish "new" from "overwrite").
//
// Mode 0400 matches cmd/vmmd/main.go::loadNodeSigningKey's strict
// 0400 perm check (line 151). Mode 0444 matches the canonical
// cosign pub-key install. A future PR may narrow pub-key mode to
// 0440 root:gregale to match sign-keys (the cosign pair) — for now
// 0444 keeps the operator's verification artifact world-readable
// and matches what schedd's parsePublicKeyPEM accepts.
func writeNodeKeyFiles(privPath, pubPath string, privPEM, pubPEM []byte, force bool) error {
	if !force {
		for _, p := range []string{privPath, pubPath} {
			if _, err := os.Stat(p); err == nil {
				return fmt.Errorf("refusing to overwrite %s (use --force for rotate)", p)
			} else if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("stat %s: %w", p, err)
			}
		}
	}
	// Strict modes (priv 0o400, pub 0o444) don't grant the owner
	// the write bit, so os.WriteFile against an existing file with
	// these modes returns EPERM on both Linux and macOS (Linux:
	// "Permission denied" when O_TRUNC is set against a file
	// whose mode lacks owner-write; macOS APFS: same). Rotate
	// (force=true) therefore unlinks both files first, then
	// re-creates them at the strict mode. The unlink is best-
	// effort: if it fails (someone else owns it, or it's been
	// mounted read-only), the subsequent WriteFile surfaces the
	// real error.
	for _, p := range []string{privPath, pubPath} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("unlink %s before write: %w", p, err)
		}
	}
	if err := os.WriteFile(privPath, privPEM, 0o400); err != nil {
		return fmt.Errorf("write private key %s (mode 0400): %w", privPath, err)
	}
	// Belt-and-braces: re-assert the strict mode in case the
	// umask or filesystem mounted us with a wider perm. Mirrors the
	// chmod pattern in cosign's WriteKeyPairForGroup.
	if err := os.Chmod(privPath, 0o400); err != nil {
		return fmt.Errorf("chmod priv %s to 0o400 after write: %w", privPath, err)
	}
	if err := os.WriteFile(pubPath, pubPEM, 0o444); err != nil {
		// Best-effort cleanup: if the pub write fails, the priv
		// is on disk at 0400 with no matching pub. Roll back so
		// the operator's next init isn't silently accepted (the
		// priv path now exists → refuse-to-overwrite kicks in).
		_ = os.Remove(privPath)
		return fmt.Errorf("write public key %s (mode 0444): %w", pubPath, err)
	}
	if err := os.Chmod(pubPath, 0o444); err != nil {
		_ = os.Remove(privPath)
		_ = os.Remove(pubPath)
		return fmt.Errorf("chmod public key %s to 0o444 after write: %w", pubPath, err)
	}
	return nil
}

// cmdNodeKeyInit writes a fresh keypair. Refuses overwrite unless
// --force is set (but the operator is expected to use `node-key
// rotate` for that flow — init --force skips the rename of any
// existing files that rotate would perform in a future patch).
func cmdNodeKeyInit(args []string) int {
	fs, f := newNodeKeyFlags("node-key init", false)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregalectl node-key init [flags]", "node-key")
		return 1
	}
	if err := writeNodeKeyPair(f.force, f.nodeKey, f.nodePub); err != nil {
		return printErr("node-key init failed", err)
	}
	keyID, err := reportKeyIDForFile(f.nodePub)
	if err != nil {
		// Non-fatal: the key is on disk, but the operator should
		// know that the key_id couldn't be computed (parse
		// failure). Log and continue.
		PrintOK(osStdout, "Wrote %s (0400 root:root) and %s (0444)\n  key_id: <compute failed: %v>\n  Next: systemctl restart gregale-vmmd (publishes the public half to schedd)",
			f.nodeKey, f.nodePub, err)
		return 0
	}
	PrintOK(osStdout, "Wrote %s (0400 root:root) and %s (0444)\n  key_id: %s\n  Next: systemctl restart gregale-vmmd (publishes the public half to schedd)",
		f.nodeKey, f.nodePub, keyID)
	return 0
}

// cmdNodeKeyRotate is the documented operator flow for replacing
// the keypair (compromise, scheduled rotation). Default force=true.
// The operator is expected to have archived the old pub key before
// running this — the verifier side has no rollback path; once
// schedd's NodeKeyRegistry.Refresh picks up the new key_id, old
// reports under the old key_id stop verifying (codes.Unauthenticated
// + capacity_signature_rejected_total counter increment).
func cmdNodeKeyRotate(args []string) int {
	fs, f := newNodeKeyFlags("node-key rotate", true)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregalectl node-key rotate [flags]", "node-key")
		return 1
	}
	if err := writeNodeKeyPair(f.force, f.nodeKey, f.nodePub); err != nil {
		return printErr("node-key rotate failed", err)
	}
	PrintOK(osStdout, "Rotated %s and %s (force=%t)\n  Restart: systemctl restart gregale-vmmd\n  Schedd NodeKeyRegistry refreshes on the next 'compute_node_changed' pg_notify tick.",
		f.nodeKey, f.nodePub, f.force)
	return 0
}

// cmdNodeKeyStatus reports mode + fingerprint + key_id for both
// files. Used by ansible stat-asserts at deploy time (the
// control_plane_service role asserts node.key is mode 0400 root:root
// — see deploy/ansible/roles/control_plane_service/tasks/main.yml)
// and by operators during incident response. Output is
// line-oriented; missing files print an explicit "missing" line
// and the command returns 0 so the operator sees both paths even
// if one is absent.
func cmdNodeKeyStatus(args []string) int {
	fs, f := newNodeKeyFlags("node-key status", false)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregalectl node-key status [flags]", "node-key")
		return 1
	}
	for _, p := range []struct {
		path  string
		label string
	}{
		{f.nodeKey, "node.key"},
		{f.nodePub, "node.pub "},
	} {
		reportNodeKeyStatus(osStdout, p.label, p.path)
	}
	return 0
}

// reportNodeKeyStatus prints one line per file: <label>  <mode>
// <sha256[:12] of bytes>  <path>. The mode is read with os.Stat;
// the fingerprint is read with os.ReadFile (NOT
// cosign.LoadPrivateKeyFile — that refuses insecure modes and a
// misconfigured file should still report what it has).
func reportNodeKeyStatus(w io.Writer, label, path string) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			_, _ = fmt.Fprintf(w, "%s  missing  %s\n", label, path)
			return
		}
		_, _ = fmt.Fprintf(w, "%s  stat error: %v  %s\n", label, err, path)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		_, _ = fmt.Fprintf(w, "%s  mode %#o  read error: %v  %s\n", label, info.Mode().Perm(), err, path)
		return
	}
	sum := sha256.Sum256(data)
	_, _ = fmt.Fprintf(w, "%s  %#o  sha256:%s  %s\n", label, info.Mode().Perm(), hex.EncodeToString(sum[:6]), path)
}

// reportKeyIDForFile parses the PEM-encoded PUBLIC KEY at path and
// returns sched.KeyIDForPublicKey's result — the same key_id the
// wire's node_key_id carries and the schedd-side registry is keyed
// by. Used by cmdNodeKeyInit so the operator can confirm at a glance
// that the freshly-generated key matches what schedd will register.
func reportKeyIDForFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return "", fmt.Errorf("%s: not PEM-encoded", path)
	}
	if block.Type != "PUBLIC KEY" {
		return "", fmt.Errorf("%s: PEM type %q, want PUBLIC KEY", path, block.Type)
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse PKIX: %w", err)
	}
	ec, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return "", fmt.Errorf("not ECDSA (got %T)", pub)
	}
	if ec.Curve != elliptic.P256() {
		return "", fmt.Errorf("curve %s, want P-256", ec.Curve.Params().Name)
	}
	return sched.KeyIDForPublicKey(ec)
}
