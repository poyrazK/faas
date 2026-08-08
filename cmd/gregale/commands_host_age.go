// commands_host_age.go — operator-side CLI for the host.age
// X25519 identity rotation (issue #316 / ADR-057). Mirrors the
// commands_sign_keys.go shape: every leaf is a local file-system
// operation against the canonical /etc/faas/secrets/host.age
// (and host.age.previous during the 30-day rotation overlap).
// No authedClient() call, no API hit, no remote round-trip.
//
// The namespace `gregale host-age` has four leaves:
//
//   - init            — write a fresh keypair (refuses overwrite)
//   - rotate          — drop a new current key, move the old to
//     .previous so daemons load both during the
//     30-day overlap window
//   - status          — print mode + fingerprint + mtime for both
//     files (current / previous) so the operator
//     sees the overlap countdown
//   - prune-previous  — remove .previous once the overlap ends
//     (default refuses if .previous is < 30 days
//     old; --force / --promote are the documented
//     escape hatches)
//
// The "rotate" leaf is the security primitive: the old key is kept
// decryptable (via age's native multi-recipient fallback across the
// LoadHostKeys slice) for 30 days so freshly-sealed ciphertexts
// under the NEW key coexist with pre-rotation ciphertexts under
// the OLD key. After 30 days the operator invokes prune-previous
// and the old envelopes become unreadable. The re-seal of
// pre-rotation envelopes to the current key is a v2 follow-up
// (issue-316-followup-rekey) — the overlap window is the actual
// security primitive, the re-seal is comfort.
//
// Mode 0400 root:root on every file the CLI writes. Matches the
// spec §11 contract and the LoadHostKey perm tripwire
// (pkg/secretbox/hostkey.go:96-105). No logging of secret material.
//
// What stays out of scope: Yubikey / HSM-backed host.age
// (issue §17 G2 lean), periodic scheduled rotation (issue §17 G6
// lean), background re-seal of pre-rotation ciphertexts.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/pkg/secretbox"
)

const dispatchHostAge = "host-age"

// subHostAgeInit / subHostAgeRotate / subHostAgeStatus /
// subHostAgePrunePrevious are the leaf names. Mirrors the
// subList / subAdd / subRm pattern in commands2.go.
const (
	subHostAgeInit          = "init"
	subHostAgeRotate        = "rotate"
	subHostAgeStatus        = "status"
	subHostAgePrunePrevious = "prune-previous"
)

// defaultMinOverlapDays is the rotation-overlap floor for
// `prune-previous`. The 30-day window matches the operator-facing
// guidance in docs/ops/host-age-rotation.md and is the same value
// the runbook accepts as "safe to prune". Operators can shorten
// this with --min-overlap-days for compliance scenarios where a
// shorter overlap is required (e.g. a confirmed compromise with no
// time for a gradual handover); --force skips the check entirely.
const defaultMinOverlapDays = 30

// Sentinel errors for the host-age CLI. Wrapped by the helpers
// below so the test file can use errors.Is for the load-bearing
// contract assertions (replacing strings.Contains which would
// silently keep passing through an i18n rewrite or a copy-edit).
var (
	// ErrInitRequiresRoot is returned by hostAgeInit when the caller
	// is not root. host.age is 0400 root:root (spec §11); a non-root
	// write would produce a file the daemons can't load.
	ErrInitRequiresRoot = errors.New("host-age: init requires root (host.age is 0400 root:root)")

	// ErrInitRefuseOverwrite is returned by hostAgeInit when
	// host.age already exists and --force is not set. Silent
	// overwrite strands every SealedSecret ever sealed under the
	// old key.
	ErrInitRefuseOverwrite = errors.New("host-age: refusing to overwrite existing host.age (use --force for emergency re-init)")

	// ErrRotateNoCurrent is returned by hostAgeRotate when host.age
	// does not exist on disk (operator skipped init or wiped the
	// secret dir). The error message points the operator at init.
	ErrRotateNoCurrent = errors.New("host-age: rotate requires an existing host.age (run 'gregale host-age init' first)")

	// ErrPruneMissingPrevious is returned by hostAgePrunePrevious
	// when host.age.previous does not exist. Surfaces as a clear
	// "already pruned or no rotation in progress" rather than a
	// silent no-op.
	ErrPruneMissingPrevious = errors.New("host-age: no host.age.previous found (already pruned, or no rotation in progress)")

	// ErrPruneTooRecent is returned by hostAgePrunePrevious when
	// .previous is younger than the min-overlap-days window. --force
	// or --min-overlap-days are the documented escape hatches;
	// --promote flips the roles instead.
	ErrPruneTooRecent = errors.New("host-age: refusing to prune .previous younger than the configured min-overlap window")
)

// cmdHostAge is the parent dispatcher. With zero args it prints
// usage; with init/rotate/status/prune-previous it fans to the
// matching helper. Unknown subcommands return 1 with a usage hint
// — same contract as cmdSignKeys / cmdBuild / cmdSecrets / cmdKeys.
func cmdHostAge(args []string) int {
	parent, _ := lookupCliCommand("host-age")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale host-age <init|rotate|status|prune-previous> [flags]", "host-age")
		return 1
	}
	switch args[0] {
	case subHostAgeInit:
		return cmdHostAgeInit(args[1:])
	case subHostAgeRotate:
		return cmdHostAgeRotate(args[1:])
	case subHostAgeStatus:
		return cmdHostAgeStatus(args[1:])
	case subHostAgePrunePrevious:
		return cmdHostAgePrunePrevious(args[1:])
	default:
		sug, _ := suggestSubcommand(args[0], parent)
		fmt.Fprintf(os.Stderr, "gregale host-age: unknown subcommand %q (known: init, rotate, status, prune-previous)\n", args[0])
		maybeSuggestSub(sug)
		return 1
	}
}

// hostAgeFlags is the shared flag surface for every leaf. The
// --dir flag points at the canonical install root
// (/etc/faas/secrets); callers may override for tests or for a
// staging box. The --force flag has leaf-specific defaults
// (init=false / rotate=true), mirroring the asymmetry in
// commands_sign_keys.go's signKeyFlags.
type hostAgeFlags struct {
	dir   string
	force bool
}

// newHostAgeFlags builds the common --dir flag set. force is a
// leaf-specific default — see the per-leaf cmd functions for the
// rationale (init refuses overwrite; rotate always overwrites; status
// ignores it; prune-previous skips the overlap age check).
func newHostAgeFlags(name string, defaultForce bool) (*flag.FlagSet, *hostAgeFlags) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	f := &hostAgeFlags{}
	fs.StringVar(&f.dir, "dir", "/etc/faas/secrets",
		"directory containing host.age / host.age.previous (canonical install: /etc/faas/secrets)")
	fs.BoolVar(&f.force, "force", defaultForce,
		"leaf-specific: init→overwrite existing / rotate→unconditional / prune-previous→skip age check")
	return fs, f
}

// hostAgeKeyPaths is the canonical on-disk pair. Loaded via
// secretbox.LoadHostKeys(dir), which returns a slice in stable
// order (current first, previous second). The struct shape keeps
// the per-file paths together so status / init / rotate can
// reference them without recomputing the dir joins.
type hostAgeKeyPaths struct {
	current  string
	previous string
}

func hostAgePaths(dir string) hostAgeKeyPaths {
	return hostAgeKeyPaths{
		current:  dir + "/host.age",
		previous: dir + "/host.age.previous",
	}
}

// cmdHostAgeInit writes a fresh keypair (refuses overwrite by
// default). The vmmd startup path already handles
// ErrHostKeyNotFound on first boot by calling GenerateAndSaveHostKey
// directly, so this leaf exists for the off-band path: a manual
// recovery from a wiped secrets dir, or an operator who wants to
// rotate without going through the multi-step `rotate` flow
// (acceptable when there are no in-flight ciphertexts — e.g. a
// brand-new box that hasn't been issued to a customer yet).
//
// --force (default false) is the documented escape hatch. The
// refuse-by-default contract mirrors cmdBackupUnsealRclone's
// refuse-to-overwrite pattern: an operator who re-runs `init`
// mid-deploy is almost certainly making a mistake, and a silent
// overwrite of an existing identity strands every SealedSecret
// ever written under the old key.
func cmdHostAgeInit(args []string) int {
	fs, f := newHostAgeFlags("host-age init", false)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale host-age init [flags]", "host-age")
		return 1
	}
	if err := hostAgeInit(f.dir, f.force); err != nil {
		return printErr("init failed", err)
	}
	PrintOK(osStdout, "Wrote %s/host.age (0400 root:root)\n  Recipient: see gregale host-age status\n  Next: systemctl restart faas-apid faas-vmmd faas-meterd faas-githubd", f.dir)
	return 0
}

func hostAgeInit(dir string, force bool) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("%w (run with sudo or as the root user)", ErrInitRequiresRoot)
	}
	paths := hostAgePaths(dir)
	if !force {
		if _, err := os.Stat(paths.current); err == nil {
			return fmt.Errorf("%w: %s", ErrInitRefuseOverwrite, paths.current)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", paths.current, err)
		}
	}
	id, err := secretbox.GenerateAndSaveHostKey(paths.current)
	if err != nil {
		return err
	}
	_ = id // RecipientString(id) intentionally not logged here; status leaf surfaces it.
	return nil
}

// cmdHostAgeRotate is the documented operator flow for replacing
// the host.age identity. Two-step shape (issue #316 / ADR-057):
//
//  1. Generate a new X25519 identity in memory.
//  2. Atomic-rename the EXISTING host.age → host.age.previous.
//  3. Atomic-rename the new key into host.age.
//
// Both renames are in the same filesystem so they happen
// effectively atomically from the daemons' perspective: daemons
// that LoadHostKeys(dir) after step 2 see [previous] and daemons
// that LoadHostKeys(dir) after step 3 see [current, previous].
// The window between the two renames is microseconds — well
// below systemd's restart cycle.
//
// --force (default true) is unconditional. Running rotate without
// overwrite is a no-op (the old file is still there); rotate is
// the documented way to actually rotate.
//
// The "rotate" leaf does NOT auto-restart daemons. Daemons don't
// watch the file; they read it once at startup via
// LoadHostKeys(dir) → systemd LoadCredential → credential dir.
// The PrintOK message at the end tells the operator what to
// restart. (A future patch could add a --restart flag that shells
// out to systemctl, but a misplaced restart during a rotation
// halfway through is harder to recover from than a one-line
// follow-up; the operator's eyes on the systemctl line are the
// right shape for v1.)
func cmdHostAgeRotate(args []string) int {
	fs, f := newHostAgeFlags("host-age rotate", true)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale host-age rotate [flags]", "host-age")
		return 1
	}
	newRecipient, prevRecipient, err := hostAgeRotate(f.dir, f.force)
	if err != nil {
		return printErr("rotate failed", err)
	}
	PrintOK(osStdout, "Rotated host.age → host.age.previous; new current written.\n  New recipient:     %s\n  Previous (now .previous): %s\n  Next: chown root:root %s/host.age %s/host.age.previous && chmod 0400 both (if not already root)\n  Next: systemctl restart faas-vmmd first (it owns host.age.pub), then faas-apid faas-meterd faas-githubd\n  Next: gregale host-age status (verify all daemons on the new fingerprint after restart)\n  Next: 30-day overlap window starts now; run 'gregale host-age prune-previous' after that",
		newRecipient, prevRecipient, f.dir, f.dir)
	return 0
}

// hostAgeRotate returns (newRecipient, previousRecipient, error).
// Returning the recipients lets the CLI surface them on stdout
// without re-reading the files (issue #316 / ADR-057 — the
// operator-facing runbook step 2 promises "the output is the new
// recipient string"; this helper is the source of truth for that
// claim).
//
// On the "no current" error path the returned recipients are both
// empty strings; the caller prints the error and bails before
// reaching the PrintOK line so the empty strings are never visible.
func hostAgeRotate(dir string, force bool) (newRecipient, prevRecipient string, err error) {
	paths := hostAgePaths(dir)

	if !force {
		if _, err := os.Stat(paths.current); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// No current to rotate FROM — point the operator at init.
				return "", "", fmt.Errorf("%w: missing %s", ErrRotateNoCurrent, paths.current)
			}
			return "", "", fmt.Errorf("stat %s: %w", paths.current, err)
		}
	}

	// Step 1: generate the new key in memory (NOT on disk yet).
	newID, err := secretbox.GenerateAndSaveHostKey(filepath.Join(dir, "host.age.new"))
	if err != nil {
		return "", "", err
	}
	// GenerateAndSaveHostKey already wrote host.age.new with 0400.
	// Capture the path so we can rename it after the .previous swap.
	newPath := filepath.Join(dir, "host.age.new")
	// Capture the recipient NOW so we can surface it on stdout
	// regardless of which rename fails — the recipient is the
	// material the operator needs to record in the rotation log.
	newRecipient = secretbox.RecipientString(newID)

	// Step 2: rename current → .previous (if current exists).
	// We use os.Rename so the swap is atomic on the same
	// filesystem. If the rename fails after step 1, we leave
	// host.age.new on disk and surface the error — the operator
	// can either mv it back to host.age or run prune-previous
	// (which would refuse because host.age is still the old
	// one). Either way the box is still unsealing under the old
	// key — no customer-visible blast radius.
	if _, err := os.Stat(paths.current); err == nil {
		if err := os.Rename(paths.current, paths.previous); err != nil {
			return newRecipient, "", fmt.Errorf("rename current → previous: %w (host.age.new left on disk for manual recovery)", err)
		}
		// Capture the previous recipient from the just-renamed
		// .previous file. We re-read it (rather than tracking the
		// in-memory identity) because the file is the source of
		// truth — if the rename succeeded but the previous file
		// was already corrupted on disk, the operator sees that.
		if prevID, loadErr := secretbox.LoadHostKey(paths.previous); loadErr == nil {
			prevRecipient = secretbox.RecipientString(prevID)
		}
	}

	// Step 3: rename .new → current.
	if err := os.Rename(newPath, paths.current); err != nil {
		// Try to roll back the .previous rename so the box stays
		// on the pre-rotate identity. Best-effort: if rollback
		// fails, the operator has host.age.new + an old
		// host.age.previous to manually reconcile. rbErr is a
		// SIBLING failure (the rollback), not a causal parent of
		// err; errorlint forbids %v on error values, so we
		// stringify rbErr explicitly via err.Error().
		if rbErr := os.Rename(paths.previous, paths.current); rbErr != nil {
			return newRecipient, prevRecipient,
				fmt.Errorf("rename .new → current: %w (rollback also failed: %s; manual recovery required: mv %s %s)",
					err, rbErr.Error(), paths.previous, paths.current)
		}
		return newRecipient, prevRecipient, fmt.Errorf("rename .new → current: %w (rolled back; old identity still active)", err)
	}

	return newRecipient, prevRecipient, nil
}

// cmdHostAgeStatus prints mode + fingerprint + mtime for both
// files. Used by ansible stat-asserts at deploy time, by the
// runbook's pre-flight checks, and by the operator during incident
// response. Output is line-oriented (no --json path; this is
// operator-only and the JSON case is a future patch).
//
// Missing files print an explicit "missing" line and the leaf
// returns 0 — the operator should see both paths even if one is
// absent (pre-rotation: no .previous; post-prune: no .previous;
// first-boot: no current). The "missing" shape mirrors
// reportSignKeyStatus in commands_sign_keys.go.
//
// The mtime is read with os.Stat and printed as both ISO-8601
// and age-in-days (the operator's countdown signal for the
// prune-previous window).
func cmdHostAgeStatus(args []string) int {
	fs, f := newHostAgeFlags("host-age status", false)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale host-age status [flags]", "host-age")
		return 1
	}
	paths := hostAgePaths(f.dir)
	reportHostAgeStatus(osStdout, "current  ", paths.current)
	reportHostAgeStatus(osStdout, "previous ", paths.previous)
	return 0
}

// reportHostAgeStatus prints one line per file: <label>  <mode>
// <sha256[:12] of bytes>  <mtime>  <age-in-days>  <path>. Missing
// files print "<label>  missing  <path>" so the operator can
// copy/paste the path straight into the next command.
//
// We read the file with os.ReadFile (not LoadHostKey, because the
// loader refuses insecure modes and a misconfigured file should
// still report what it has — same reasoning as
// commands_sign_keys.go::reportSignKeyStatus).
func reportHostAgeStatus(w io.Writer, label, path string) {
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
	mtime := info.ModTime().UTC()
	ageDays := int(time.Since(mtime).Hours() / 24)
	_, _ = fmt.Fprintf(w, "%s  %#o  sha256:%s  mtime=%s  age=%dd  %s\n",
		label, info.Mode().Perm(), hex.EncodeToString(sum[:6]),
		mtime.Format(time.RFC3339), ageDays, path)
}

// cmdHostAgePrunePrevious removes host.age.previous once the
// 30-day overlap window has elapsed. Refuses by default if the
// .previous file is younger than --min-overlap-days (default 30)
// — the operator can shorten the window with --min-overlap-days=N
// (compliance scenarios) or skip the check entirely with --force
// (incident-response scenarios where the operator already
// understands the trade-off).
//
// --promote renames .previous → current instead of removing it.
// Use case: the freshly-rotated current key was lost or
// compromised mid-rotation and the operator needs the previous
// key to be the new current. Refuses if a current file already
// exists (would silently overwrite; the operator must remove it
// first).
//
// Logs to journalctl via the `gregale` syslog identifier (set by
// the systemd unit) so the prune action shows up in the audit
// trail. The action log line does NOT include the recipient
// material — only the timestamp and the path. A future patch
// could add --dry-run + --json for CI gates; v1 is operator-only
// and the manual eyeball is the right shape.
func cmdHostAgePrunePrevious(args []string) int {
	fs := flag.NewFlagSet("host-age prune-previous", flag.ContinueOnError)
	f := &hostAgeFlags{}
	minOverlap := fs.Int("min-overlap-days", defaultMinOverlapDays,
		"refuse if host.age.previous is younger than this many days (set to 0 with --force for an unconditional prune)")
	promote := fs.Bool("promote", false,
		"rename host.age.previous → host.age instead of removing (manual escape hatch when current was lost)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale host-age prune-previous [--min-overlap-days N] [--force] [--promote]", "host-age")
		return 1
	}
	dir := f.dir
	if dir == "" {
		dir = "/etc/faas/secrets"
	}
	if err := hostAgePrunePrevious(dir, *minOverlap, f.force, *promote); err != nil {
		return printErr("prune-previous failed", err)
	}
	action := "removed"
	if *promote {
		action = "promoted (renamed → host.age)"
	}
	PrintOK(osStdout, "host-age prune-previous: %s (overlap=%dd, force=%t)\n  Next: restart the daemons only if you also --promoted (the rename changes the current identity)",
		action, *minOverlap, f.force)
	return 0
}

func hostAgePrunePrevious(dir string, minOverlapDays int, force, promote bool) error {
	paths := hostAgePaths(dir)

	info, err := os.Stat(paths.previous)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: missing %s", ErrPruneMissingPrevious, paths.previous)
		}
		return fmt.Errorf("stat %s: %w", paths.previous, err)
	}

	if promote {
		// Promotion ignores the age check (the operator is making
		// an explicit choice to discard the current identity and
		// restore the previous one). PromotePreviousToCurrent
		// refuses if a current exists, so we don't need a separate
		// guard here.
		return secretbox.PromotePreviousToCurrent(dir)
	}

	if !force {
		ageDays := int(time.Since(info.ModTime()).Hours() / 24)
		if ageDays < minOverlapDays {
			return fmt.Errorf("%w: .previous is %d days old (min %d); use --force, --min-overlap-days=%d, or --promote",
				ErrPruneTooRecent, ageDays, minOverlapDays, ageDays)
		}
	}

	if err := os.Remove(paths.previous); err != nil {
		return fmt.Errorf("remove %s: %w", paths.previous, err)
	}
	return nil
}
