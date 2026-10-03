// commands_backup.go — operator-side CLI for off-host pg backup
// wiring (issue #250 / ADR-056). Sibling surface to commands_sign_keys
// (cosign keypair) and commands_pki (mTLS trust root): every leaf is
// a local file-system operation against the canonical
// /etc/faas/secrets/storage-box/ paths (or caller-supplied
// --in / --out flags). No authedClient() call, no API hit.
//
// The namespace is `gregalectl backup` with three leaves today:
//
//   - init   — create /etc/faas/secrets/storage-box/ (0700 root:root)
//     and write the two known-placeholder stub files the doctor
//     detects (commands_doctor.go:1274,1283,1292): rclone.conf
//     (0400 root:root) and archive-creds.json (0400 root:root).
//     Refuses to overwrite an existing layout unless --force is
//     passed (the storage-box dir carries the box-age-key that
//     identities the operator-laptop unseal path; silently
//     clobbering it strands every sealed envelope). Does NOT
//     write host.age / session.key (those live in `secrets init`,
//     which is the canonical first-boot path for a control-plane
//     node); does NOT write the box-age-key itself (the operator
//     provides their own age identity). The intent is "operator
//     can land the storage-box side of the layout without a full
//     secrets init" — e.g. a compute-only box that already has
//     host.age shipped from the control plane.
//
//   - unseal-rclone  — decrypt a host.age-sealed `rclone.conf` envelope
//     using the on-box age identity (box-age-key) and write the
//     plaintext to /etc/faas/secrets/storage-box/rclone.conf (mode
//     0400 root:root; systemd stages it for the PostgreSQL cluster.
//     Refuses to overwrite an existing plaintext unless
//     --force is passed (re-unsealing is a deliberate rotation
//     step, not a bootstrap-time side effect).
//
//   - unseal-archive-creds — same shape as unseal-rclone but for the
//     log-archive S3 credentials. Mirrors the JSON-shape sanity
//     check on the decrypted plaintext (issue #562 PR-A).
//
// The age identity path defaults to
// /etc/faas/secrets/storage-box/box-age-key (the canonical install
// site written by the v1 bootstrap.sh step 11d — RETIRED 2026-08-15
// by issue #911 / PR-1; the v2 path is PR-X `gregalectl secrets init`).
// The input envelope path defaults to /root/rclone.conf.age — the
// staging location where the operator scp's the .age envelope. Output
// defaults to /etc/faas/secrets/storage-box/rclone.conf.
//
// The unseal deliberately uses the locally-stored age identity (NOT
// the on-host host.age key) so the on-disk `rclone.conf` can be
// re-sealed and rotated independently of the host.age key that
// protects per-account TOTP envelopes. Two secrets, two identities,
// two rotation cadences — same shape as the cosign sign-keypair
// (commands_sign_keys.go) being separate from the host.age.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"filippo.io/age"
)

// Canonical install paths for off-host pg backup secrets. These
// match the v1 bootstrap.sh step 11d staging convention (RETIRED
// 2026-08-15 by issue #911 / PR-1; v2 path is PR-X `gregalectl secrets
// init`) and the ansible role's stat-assert
// (postgres_backup/tasks/main.yml).
const (
	defaultStorageBoxDir = "/etc/faas/secrets/storage-box"
	defaultRcloneConf    = defaultStorageBoxDir + "/rclone.conf"
	defaultBoxAgeKey     = defaultStorageBoxDir + "/box-age-key"
	defaultRcloneAgeIn   = "/root/rclone.conf.age"
)

const dispatchBackup = "backup"

const (
	subUnsealRclone = "unseal-rclone"
	subBackupInit   = "init"
)

func cmdBackup(args []string) int {
	parent, _ := lookupCliCommand("backup")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregalectl backup <subcommand> [flags]\n  known subcommands: init, unseal-rclone, unseal-archive-creds", "backup")
		return 1
	}
	switch args[0] {
	case subBackupInit:
		return cmdBackupInit(args[1:])
	case subUnsealRclone:
		return cmdBackupUnsealRclone(args[1:])
	case subUnsealArchiveCreds:
		return cmdBackupUnsealArchiveCreds(args[1:])
	default:
		sug, _ := suggestSubcommand(args[0], parent)
		fmt.Fprintf(os.Stderr, "gregalectl backup: unknown subcommand %q (known: init, unseal-rclone, unseal-archive-creds)\n", args[0])
		maybeSuggestSub(sug)
		return 1
	}
}

type unsealRcloneFlags struct {
	ageIdentity string
	in          string
	out         string
	force       bool
}

func newUnsealRcloneFlags(name string) (*flag.FlagSet, *unsealRcloneFlags) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	f := &unsealRcloneFlags{}
	fs.StringVar(&f.ageIdentity, "age-identity", defaultBoxAgeKey,
		"path to the box-local age identity used to decrypt the rclone.conf envelope")
	fs.StringVar(&f.in, "in", defaultRcloneAgeIn,
		"path to the host.age-sealed rclone.conf envelope (typically scp'd to /root/rclone.conf.age)")
	fs.StringVar(&f.out, "out", defaultRcloneConf,
		"path to write the decrypted rclone.conf (mode 0400 root:root)")
	fs.BoolVar(&f.force, "force", false,
		"overwrite an existing plaintext rclone.conf (rotation flow only)")
	return fs, f
}

// cmdBackupUnsealRclone decrypts a host.age-sealed rclone.conf
// envelope using the box-local age identity and writes the
// plaintext to /etc/faas/secrets/storage-box/rclone.conf (mode
// 0400 root:root). This is the unseal side of the v1 bootstrap.sh
// step 11d handshake (RETIRED 2026-08-15 by issue #911 / PR-1;
// v2 path is PR-X `gregalectl secrets init`): the operator scp's the
// .age envelope to /root/, gregalectl backup unseal-rclone decrypts
// it, then shreds the envelope so a future host.age-key compromise
// can't replay it.
//
// Refuses to overwrite an existing plaintext unless --force is
// passed. Mirrors the cosign sign-keys init flow (refuse rotation
// by default — the operator uses the rotate subcommand for that),
// but rolled into a single subcommand here because the unseal
// step has no public-key side to mirror.
func cmdBackupUnsealRclone(args []string) int {
	fs, f := newUnsealRcloneFlags("backup unseal-rclone")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregalectl backup unseal-rclone [flags]", "backup")
		return 1
	}
	if err := unsealRclone(f); err != nil {
		return printErr("unseal failed", err)
	}
	PrintOK(osStdout, "Wrote %s (0400 root:root)\n  Next: systemctl daemon-reload && systemctl restart postgresql@<major>-main faas-pg-basebackup-push",
		f.out)
	return 0
}

// unsealRclone reads the .age envelope, decrypts it with the
// box-local age identity, and atomically writes the plaintext to
// the destination. The atomic-write dance (tmp + rename) avoids
// half-written plaintexts on the canonical install path — a
// truncated rclone.conf makes the push unit hang on rclone's
// "config not found" message indefinitely, which is harder to
// spot than a missing file. Mode 0400 root:root on the final file matches
// the systemd credential source contract: PID 1 reads it and stages a
// service-scoped copy for the PostgreSQL cluster.
func unsealRclone(f *unsealRcloneFlags) error {
	if !f.force {
		if _, err := os.Stat(f.out); err == nil {
			return fmt.Errorf("refusing to overwrite existing %s (use --force for rotation)", f.out)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat destination: %w", err)
		}
	}

	identityData, err := os.ReadFile(f.ageIdentity)
	if err != nil {
		return fmt.Errorf("read age identity %s: %w", f.ageIdentity, err)
	}
	identity, err := parseBoxAgeIdentity(identityData)
	if err != nil {
		return fmt.Errorf("parse age identity: %w", err)
	}

	envelopeData, err := os.ReadFile(f.in)
	if err != nil {
		return fmt.Errorf("read envelope %s: %w", f.in, err)
	}
	envelopeR := bytes.NewReader(envelopeData)

	plaintextR, err := age.Decrypt(envelopeR, identity)
	if err != nil {
		return fmt.Errorf("decrypt envelope (wrong box-age-key?): %w", err)
	}

	// Atomic write: tmp in the destination directory, rename(2)
	// into place. The destination directory must exist (bootstrap
	// creates it with mode 0700 root:root in step 11d before calling
	// this command). We don't MkdirAll here — a missing dir means
	// the role hasn't run, and silently creating it would mask
	// that failure mode from the operator.
	tmp, err := os.CreateTemp(filepath.Dir(f.out), ".rclone.conf.tmp.*")
	if err != nil {
		return fmt.Errorf("create tmp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		// Best-effort cleanup if rename didn't happen.
		_ = os.Remove(tmpName)
	}()

	if _, err := io.Copy(tmp, plaintextR); err != nil {
		// Best-effort close: the tmp file is unlinked by the
		// deferred os.Remove above; a stuck close on the error path
		// would only delay that cleanup. The error we surface is
		// the io.Copy error, not a close error — pinning the close
		// would mask the real failure.
		_ = tmp.Close()
		return fmt.Errorf("write plaintext: %w", err)
	}
	if err := tmp.Chmod(0o400); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod 0400: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmpName, f.out); err != nil {
		return fmt.Errorf("rename into place: %w", err)
	}
	return nil
}

// backupInitFlags is the flag surface for `backup init`. Mirrors the
// secrets-init flag set (commands_secrets_init.go:secretsInitFlags) so
// the operator learns one shape across both namespaces. The
// canonical install path is /etc/faas/secrets/storage-box; --dir
// exists only so an operator can lay the layout down on a non-root
// dev box (the chmod + chown calls in writeBackupInitStub go
// best-effort on a non-root caller; tests run as uid 1000 and pin
// the on-disk shape).
type backupInitFlags struct {
	dir   string
	force bool
}

func newBackupInitFlags(name string) (*flag.FlagSet, *backupInitFlags) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	f := &backupInitFlags{}
	fs.StringVar(&f.dir, "dir", defaultStorageBoxDir,
		"storage-box directory (canonical: "+defaultStorageBoxDir+")")
	fs.BoolVar(&f.force, "force", false,
		"overwrite existing stub files (refuse by default — the storage-box dir carries the box-age-key that identities the unseal path)")
	return fs, f
}

// cmdBackupInit creates the storage-box layout: the directory itself
// at 0700 root:root and the two known-placeholder stub files the
// doctor already detects (commands_doctor.go:1274,1283,1292). After
// init, the operator runs `backup unseal-rclone` + `backup
// unseal-archive-creds` to replace the placeholders with the real
// plaintext.
//
// Refuses to overwrite existing files unless --force is passed. The
// default posture mirrors `secrets init` (commands_secrets_init.go:
// ErrSecretsInitRefuseOverwrite) — silently clobbering a populated
// storage-box dir strands every sealed envelope the operator has
// previously scp'd in.
func cmdBackupInit(args []string) int {
	fs, f := newBackupInitFlags("backup init")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregalectl backup init [--dir DIR] [--force]", "backup")
		return 1
	}
	if err := backupInit(f, osStdout); err != nil {
		return printErr("backup init failed", err)
	}
	PrintOK(osStdout, "Wrote storage-box layout under %s\n  Next: scp <rclone.conf.age> to %s and run `gregalectl backup unseal-rclone`\n  Next: scp <archive-creds.json.age> to %s and run `gregalectl backup unseal-archive-creds`\n  Tip: on a control-plane node, prefer `gregalectl secrets init` (writes host.age/session.key/box-age-key + the same stubs in one batch).",
		f.dir,
		defaultRcloneAgeIn, f.dir,
		defaultRcloneAgeIn, f.dir)
	return 0
}

// backupInit is the package-private worker. Splitting it from
// cmdBackupInit lets tests exercise the file-write side without the
// flag-parse boilerplate — mirrors the secretsInit split at
// commands_secrets_init.go:166.
func backupInit(f *backupInitFlags, stdout io.Writer) error {
	if err := os.MkdirAll(f.dir, 0o700); err != nil {
		return fmt.Errorf("mkdir storage-box dir %s: %w", f.dir, err)
	}
	// Stub 1: rclone.conf placeholder (0400 root:root).
	// Mirrors commands_secrets_init.go:writeRcloneStub but lives in
	// backup's namespace because backup init is the canonical
	// storage-box-only path (no host.age / session.key side).
	if err := writeBackupRcloneStub(filepath.Join(f.dir, "rclone.conf"), f.force); err != nil {
		return err
	}
	// Stub 2: archive-creds.json placeholder (0400 root:root).
	// Mirrors commands_secrets_init.go:writeArchiveStub for the
	// same reason. The mandatory-on-disk shape is `{}` so the
	// log-archive's LoadCredential parses cleanly before the
	// operator unseals real creds.
	if err := writeBackupArchiveStub(filepath.Join(f.dir, "archive-creds.json"), f.force); err != nil {
		return err
	}
	return nil
}

// writeBackupRcloneStub writes the doctor-detected rclone.conf
// placeholder. Single-line JSON marker so the ansible stat-assert
// (postgres_backup/tasks/main.yml:198) passes pre-unseal. Mirrors
// commands_secrets_init.go:writeRcloneStub (the secrets-init
// version) but lives here so `backup init` is self-contained.
//
// force=true requires the file to be writable. The on-disk file
// lives at 0400, so a plain WriteFile would EPERM on the second
// init. We chmod 0644 first (when force=true and the file
// exists), then chmod back to 0400 after the write — same
// dance commands_secrets_init.go:writeHostAge uses via
// enforceFileMode.
func writeBackupRcloneStub(path string, force bool) error {
	exists := false
	if _, err := os.Stat(path); err == nil {
		exists = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if exists && !force {
		return fmt.Errorf("refusing to overwrite existing %s (use --force for re-init)", path)
	}
	if exists && force {
		if err := os.Chmod(path, 0o644); err != nil {
			return fmt.Errorf("chmod 0644 %s (for force re-init): %w", path, err)
		}
	}
	stub := []byte(`{"_":"backup init stub — replace via 'gregalectl backup unseal-rclone'"}`)
	if err := os.WriteFile(path, stub, 0o400); err != nil {
		return fmt.Errorf("write rclone.conf stub %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		return fmt.Errorf("chmod 0400 %s: %w", path, err)
	}
	return nil
}

// writeBackupArchiveStub writes the empty archive-creds envelope.
// Mandatory shape is `{}` so the log-archive LoadCredential parses
// before the operator unseals real creds. Mirrors
// commands_secrets_init.go:writeArchiveStub. Same force-rewrite
// dance as writeBackupRcloneStub.
func writeBackupArchiveStub(path string, force bool) error {
	exists := false
	if _, err := os.Stat(path); err == nil {
		exists = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if exists && !force {
		return fmt.Errorf("refusing to overwrite existing %s (use --force for re-init)", path)
	}
	if exists && force {
		if err := os.Chmod(path, 0o644); err != nil {
			return fmt.Errorf("chmod 0644 %s (for force re-init): %w", path, err)
		}
	}
	stub := []byte(`{}`)
	if err := os.WriteFile(path, stub, 0o400); err != nil {
		return fmt.Errorf("write archive-creds.json stub %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		return fmt.Errorf("chmod 0400 %s: %w", path, err)
	}
	return nil
}

// parseBoxAgeIdentity reads the box-age identity file in every shape it is
// produced in: a bare AGE-SECRET-KEY line with or without a trailing newline
// (cd-compute writes COMPUTE_BOX_AGE_KEY with printf '%s\n'), or age-keygen
// output with its "# created" and "# public key" comment lines. Exactly one
// X25519 identity is accepted.
func parseBoxAgeIdentity(data []byte) (*age.X25519Identity, error) {
	ids, err := age.ParseIdentities(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if len(ids) != 1 {
		return nil, fmt.Errorf("want exactly one identity, found %d", len(ids))
	}
	id, ok := ids[0].(*age.X25519Identity)
	if !ok {
		return nil, fmt.Errorf("identity is %T, want an X25519 identity", ids[0])
	}
	return id, nil
}
