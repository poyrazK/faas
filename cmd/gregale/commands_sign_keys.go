// commands_sign_keys.go — operator-side CLI for the cosign sign-key
// pair (ADR-038 §Phase 3 / Tier 3). This is the surface PR #322
// (dbf89d1) deferred to a follow-up. It is the OPERATOR surface, not
// the customer surface: there is no `authedClient()` call, no SDK,
// no API call — every leaf is a local file-system operation
// against the canonical /etc/faas/secrets/ paths (or the
// caller-supplied --sign-key / --verify-key flags).
//
// The namespace `gregale keys` is already taken by the customer-facing
// API-key manager (cmdKeys in commands2.go:725-780 — every leaf
// calls authedClient() and hits apid). Operator-side provisioning
// has no business in that namespace; this is a separate top-level
// command `gregale sign-keys` with three leaves:
//   - init   — write a fresh keypair (refuses overwrite)
//   - rotate — write a fresh keypair with --force (overwrite allowed
//     after archiving the old public key)
//   - status — print mode + fingerprint + paths for both files
//
// All three leaves share the same flag surface (--sign-key,
// --verify-key, --force). The default paths are the cosign
// package's DefaultSignKeyPath / DefaultSignPubPath, which match
// what cmd/imaged and cmd/schedd load at startup. A reviewer
// changing one of those constants must also update this file's
// --help.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/cosign"
)

const dispatchSignKeys = "sign-keys"

// subInit / subRotate / subStatus are the leaf names. Mirrors the
// subList / subAdd / subRm pattern in commands2.go.
const (
	subInit   = "init"
	subRotate = "rotate"
	subStatus = "status"
)

// cmdSignKeys is the parent dispatcher. With zero args it prints
// usage; with init/rotate/status it fans to the matching helper.
// Unknown subcommands return 1 with a usage hint — same contract
// as cmdBuild / cmdSecrets / cmdKeys.
func cmdSignKeys(args []string) int {
	parent, _ := lookupCliCommand("sign-keys")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale sign-keys <init|rotate|status> [flags]", "sign-keys")
		return 1
	}
	switch args[0] {
	case subInit:
		return cmdSignKeysInit(args[1:])
	case subRotate:
		return cmdSignKeysRotate(args[1:])
	case subStatus:
		return cmdSignKeysStatus(args[1:])
	default:
		sug, _ := suggestSubcommand(args[0], parent)
		fmt.Fprintf(os.Stderr, "gregale sign-keys: unknown subcommand %q (known: init, rotate, status)\n", args[0])
		maybeSuggestSub(sug)
		return 1
	}
}

// sharedFlags builds the common --sign-key / --verify-key flag set.
// Both init and rotate use the same surface; status only needs the
// paths (no --force).
//
// force defaults: init=false (refuse overwrite by default; an operator
// who re-runs `init` mid-deploy is almost certainly making a mistake),
// rotate=true (a bare `gregale sign-keys rotate` MUST overwrite —
// that's the whole point of the subcommand; running rotate without
// overwrite is a no-op, see cmdSignKeysRotate body for the rotation
// flow).
//
// The rotate-true default was the source of a long-standing doc bug
// (PR #449 follow-up): the previous comment claimed "does NOT
// silently overwrite" while the code passed defaultForce = true. The
// contradiction has been in this file since PR #322. The asymmetry
// is load-bearing — TestSignKeyFlagDefaults pins it.
type signKeyFlags struct {
	signKey string
	verify  string
	force   bool
}

func newSignKeyFlags(name string, defaultForce bool) (*flag.FlagSet, *signKeyFlags) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	f := &signKeyFlags{}
	fs.StringVar(&f.signKey, "sign-key", cosign.DefaultSignKeyPath,
		"path to the private key (mode 0440 root:gregale on the canonical install)")
	fs.StringVar(&f.verify, "verify-key", cosign.DefaultSignPubPath,
		"path to the public key (mode 0444, world-readable)")
	fs.BoolVar(&f.force, "force", defaultForce,
		"overwrite an existing keypair (rotate only)")
	return fs, f
}

// writeKeyPair is the shared write path. Status is the only leaf
// that doesn't call this. Both init and rotate converge here so
// any future change to the writer (e.g. switching
// WriteKeyPairForGroup → a KMS-backed writer) only has to land in
// one place. The error is annotated with the operator-facing hint
// because cmd/imaged and cmd/schedd both reference this same
// surface from their startup-error messages.
func writeKeyPair(force bool, privPath, pubPath string) error {
	privPEM, pubPEM, err := cosign.GenerateKeyPair()
	if err != nil {
		return fmt.Errorf("generate keypair: %w", err)
	}
	if err := cosign.WriteKeyPairForGroup(privPath, privPEM, pubPath, pubPEM, force); err != nil {
		return err
	}
	return nil
}

// cmdSignKeysInit writes a fresh keypair. Refuses to overwrite
// existing files. The caller is expected to be the bootstrap or
// an ansible task running as root (so the post-write chown to
// root:gregale succeeds). `gregale sign-keys init --force` is allowed
// for emergency re-init but the operator should normally use
// `gregale sign-keys rotate` for that flow — `init --force` skips
// the rename of the existing pub file that rotate performs in a
// future patch (out of scope here).
func cmdSignKeysInit(args []string) int {
	fs, f := newSignKeyFlags("sign-keys init", false)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale sign-keys init [flags]", "sign-keys")
		return 1
	}
	if err := writeKeyPair(f.force, f.signKey, f.verify); err != nil {
		return printErr("init failed", err)
	}
	PrintOK(osStdout, "Wrote %s (0440) and %s (0444)\n  Next: chown root:gregale %s && chmod 0440 %s\n  Next: chown root:root %s && chmod 0444 %s",
		f.signKey, f.verify,
		f.signKey, f.signKey,
		f.verify, f.verify)
	return 0
}

// cmdSignKeysRotate is the documented operator flow for replacing
// the keypair (compromise, scheduled rotation). Default force=true
// because rotate without overwrite is a no-op. The operator is
// expected to have archived the old pub key before running this
// (the verifier side has no rollback path; once schedd loads the
// new pub, old signatures won't verify). A `--keep-old-pub` flag
// for archive-and-rotate is a future patch; for now the operator
// `cp`s the pub file before running rotate.
func cmdSignKeysRotate(args []string) int {
	fs, f := newSignKeyFlags("sign-keys rotate", true)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale sign-keys rotate [flags]", "sign-keys")
		return 1
	}
	if err := writeKeyPair(f.force, f.signKey, f.verify); err != nil {
		return printErr("rotate failed", err)
	}
	PrintOK(osStdout, "Rotated %s and %s (force=%t)\n  Restart: systemctl restart gregale-imaged gregale-schedd",
		f.signKey, f.verify, f.force)
	return 0
}

// cmdSignKeysStatus reports the mode + fingerprint for both files.
// Used by ansible stat-asserts at deploy time and by the operator
// during incident response. Output is line-oriented (no --json
// path; this is operator-only and the JSON case is a future
// patch). Missing files print an explicit "missing" line and
// return 0 — the operator should see both paths even if one is
// absent, so they can run `init` once.
func cmdSignKeysStatus(args []string) int {
	fs, f := newSignKeyFlags("sign-keys status", false)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale sign-keys status [flags]", "sign-keys")
		return 1
	}
	for _, p := range []struct {
		path  string
		label string
	}{
		{f.signKey, "sign.key    "},
		{f.verify, "sign-pub.pem"},
	} {
		reportSignKeyStatus(osStdout, p.label, p.path)
	}
	return 0
}

// reportSignKeyStatus prints one line per file: <label>  <mode>
// <sha256[:12] of bytes>  <path>. Missing files print "<label>
// missing: <err>" so the operator can copy/paste the path straight
// into the next command. The mode is read with os.Stat; the
// fingerprint is read with os.ReadFile (not LoadPrivateKeyFile /
// LoadPublicKeyFile, because the loader refuses insecure modes and
// a misconfigured file should still report what it has).
func reportSignKeyStatus(w io.Writer, label, path string) {
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
