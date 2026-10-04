// commands_backup_test.go — pins the load-bearing contracts of the
// operator-side off-host pg backup unseal CLI (commands_backup.go).
//
// The current contracts under test:
//
//   - unsealRclone refuses to overwrite an existing plaintext unless
//     --force is passed. The refuse-by-default contract protects a
//     mid-deploy operator from accidentally rotating the rclone
//     session — a rotation against the configured off-host backend in active
//     use strands the nightly push unit until the new key is in
//     place. --force is the documented escape hatch; --force=false
//     (the default) is the safe path.
//
//   - unsealRclone rejects a wrong box-age-key: the test seals a
//     plaintext with identity A and tries to unseal with identity B.
//     We expect a decrypt error containing no plaintext bytes — the
//     decrypt failure surfaces as the wrong-key wrapper, not a
//     silent corruption.
//
//   - unsealArchiveCreds mirrors unsealRclone's contracts
//     (refuse-overwrite + wrong-key) AND adds the JSON-shape
//     sanity check: a half-decrypted envelope must surface as
//     "plaintext is not a valid archive-creds.json" rather than
//     silently ship garbage to S3 (issue #562 PR-A).
//
// All contracts are exercised with a tmpdir + a freshly-generated
// age X25519 pair so the test never touches /etc/faas/secrets/ and
// runs cleanly on a developer laptop without the production secret
// path populated.
package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
)

// generateAgeKey writes a fresh age X25519 identity to disk and
// returns its path. Mirrors the operator-laptop flow (`age-keygen
// -o box-age.key`) so the test exercises the same identity shape
// the unseal command will see in production.
func generateAgeKey(t *testing.T) string {
	t.Helper()
	ident, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity: %v", err)
	}
	path := filepath.Join(t.TempDir(), "box-age.key")
	if err := os.WriteFile(path, []byte(ident.String()), 0o400); err != nil {
		t.Fatalf("write identity: %v", err)
	}
	return path
}

// sealForRecipients seals plaintext against the X25519 recipient
// derived from the given identity. We deliberately reconstruct a
// recipient from the identity's String() form (which is "AGE
// SECRET KEY-...") by parsing it back — round-trips the same
// identity format the unseal side sees.
func sealForRecipients(t *testing.T, identityPath, plaintext string) string {
	t.Helper()
	raw, err := os.ReadFile(identityPath)
	if err != nil {
		t.Fatalf("read identity: %v", err)
	}
	ident, err := age.ParseX25519Identity(string(raw))
	if err != nil {
		t.Fatalf("ParseX25519Identity: %v", err)
	}
	recipient := ident.Recipient()
	var sealed strings.Builder
	w, err := age.Encrypt(&sealed, recipient)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := w.Write([]byte(plaintext)); err != nil {
		t.Fatalf("write plaintext: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close sealer: %v", err)
	}
	outPath := filepath.Join(t.TempDir(), "rclone.conf.age")
	if err := os.WriteFile(outPath, []byte(sealed.String()), 0o400); err != nil {
		t.Fatalf("write envelope: %v", err)
	}
	return outPath
}

// TestUnsealRclone_HappyPath seals a fake rclone.conf with a fresh
// age key, then unseals it into a tempdir. Verifies that the
// plaintext bytes round-trip and the output file ends up mode 0400
// — the mode the ansible stat-assert at
// postgres_backup/tasks/main.yml requires (review F2). systemd reads the
// root-only source and stages a service-scoped copy for PostgreSQL.
func TestUnsealRclone_HappyPath(t *testing.T) {
	identPath := generateAgeKey(t)
	const plaintext = "[offhostbox]\ntype = sftp\nhost = u123.your-storagebox.de\nuser = u123\n"
	envelope := sealForRecipients(t, identPath, plaintext)

	outPath := filepath.Join(t.TempDir(), "rclone.conf")
	f := &unsealRcloneFlags{
		ageIdentity: identPath,
		in:          envelope,
		out:         outPath,
		force:       false,
	}
	if err := unsealRclone(f); err != nil {
		t.Fatalf("unsealRclone: %v", err)
	}

	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read unsealed: %v", err)
	}
	if string(got) != plaintext {
		t.Fatalf("round-trip mismatch:\n got: %q\nwant: %q", got, plaintext)
	}

	st, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("stat out: %v", err)
	}
	if mode := st.Mode().Perm(); mode != 0o400 {
		t.Fatalf("mode: got %#o want 0400", mode)
	}
}

// TestUnsealRclone_RefuseOverwrite pins the refuse-rotate-by-default
// contract. We unseal once successfully, then attempt a second
// unseal against the same output path without --force and expect a
// refusal error. The test name encodes the contract so a future
// "let's default force to true" PR fails this test loudly.
func TestUnsealRclone_RefuseOverwrite(t *testing.T) {
	identPath := generateAgeKey(t)
	envelope := sealForRecipients(t, identPath, "first")

	outPath := filepath.Join(t.TempDir(), "rclone.conf")
	if err := unsealRclone(&unsealRcloneFlags{
		ageIdentity: identPath,
		in:          envelope,
		out:         outPath,
		force:       false,
	}); err != nil {
		t.Fatalf("first unseal: %v", err)
	}

	err := unsealRclone(&unsealRcloneFlags{
		ageIdentity: identPath,
		in:          envelope,
		out:         outPath,
		force:       false,
	})
	if err == nil {
		t.Fatal("second unseal without --force should refuse, got nil error")
	}
	if !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("refusal error: got %q, want substring 'refusing to overwrite'", err.Error())
	}
}

// TestUnsealRclone_WrongKey rejects a wrong box-age-key by sealing
// with identity A and unsealing with identity B. We expect a
// decrypt error — the wrong-key wrapper surfaces fast (no
// half-decrypted file written to disk because the atomic-rename
// happens after the full io.Copy succeeds).
func TestUnsealRclone_WrongKey(t *testing.T) {
	identA := generateAgeKey(t)
	identB := generateAgeKey(t)
	envelope := sealForRecipients(t, identA, "secret")

	outPath := filepath.Join(t.TempDir(), "rclone.conf")
	err := unsealRclone(&unsealRcloneFlags{
		ageIdentity: identB,
		in:          envelope,
		out:         outPath,
		force:       false,
	})
	if err == nil {
		t.Fatal("unseal with wrong key should fail, got nil error")
	}
	// Either the wrapper text or a substring thereof — filippo.io/age
	// emits "no identity matched any of the recipients" on a clean
	// key mismatch. We don't pin the exact text (it's upstream); we
	// pin that we got a non-nil error and the destination file was
	// never created.
	if _, statErr := os.Stat(outPath); !os.IsNotExist(statErr) {
		t.Fatalf("output file should not exist on wrong-key failure: stat err = %v", statErr)
	}
}

// TestUnsealArchiveCreds_HappyPath seals a fake archive-creds.json
// (the wire shape cmd/apid/main.go::readArchiveCreds expects) and
// asserts the round-trip bytes match and the output file ends up
// mode 0400 root:root — the perm tripwire the ansible role
// asserts (deploy/ansible/roles/control_plane_service/tasks/
// main.yml:215-235).
func TestUnsealArchiveCreds_HappyPath(t *testing.T) {
	identPath := generateAgeKey(t)
	const plaintext = `{"endpoint":"https://s3.us-east-1.amazonaws.com","region":"us-east-1","key_id":"AKIA","secret":"wJal"}` + "\n"
	envelope := sealForRecipients(t, identPath, plaintext)

	outPath := filepath.Join(t.TempDir(), "archive-creds.json")
	f := &unsealArchiveCredsFlags{
		ageIdentity: identPath,
		in:          envelope,
		out:         outPath,
		force:       false,
	}
	if err := unsealArchiveCreds(f); err != nil {
		t.Fatalf("unsealArchiveCreds: %v", err)
	}

	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read unsealed: %v", err)
	}
	if string(got) != plaintext {
		t.Fatalf("round-trip mismatch:\n got: %q\nwant: %q", got, plaintext)
	}

	st, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("stat out: %v", err)
	}
	if mode := st.Mode().Perm(); mode != 0o400 {
		t.Fatalf("mode: got %#o want 0400", mode)
	}
}

// TestUnsealArchiveCreds_RefuseOverwrite pins the
// refuse-rotate-by-default contract — same shape as
// TestUnsealRclone_RefuseOverwrite above. The unseal flow
// exists for bootstrap, not rotation; --force is the
// documented escape hatch.
func TestUnsealArchiveCreds_RefuseOverwrite(t *testing.T) {
	identPath := generateAgeKey(t)
	envelope := sealForRecipients(t, identPath, `{"key_id":"x"}`)

	outPath := filepath.Join(t.TempDir(), "archive-creds.json")
	if err := unsealArchiveCreds(&unsealArchiveCredsFlags{
		ageIdentity: identPath,
		in:          envelope,
		out:         outPath,
		force:       false,
	}); err != nil {
		t.Fatalf("first unseal: %v", err)
	}

	err := unsealArchiveCreds(&unsealArchiveCredsFlags{
		ageIdentity: identPath,
		in:          envelope,
		out:         outPath,
		force:       false,
	})
	if err == nil {
		t.Fatal("second unseal without --force should refuse, got nil error")
	}
	if !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("refusal error: got %q, want substring 'refusing to overwrite'", err.Error())
	}
}

// TestUnsealArchiveCreds_WrongKey rejects a wrong box-age-key.
// We seal with identity A and try to unseal with identity B.
// A partial decrypt must NOT leave a half-written file on disk
// — the atomic-rename happens only after io.ReadAll + JSON
// validation succeed.
func TestUnsealArchiveCreds_WrongKey(t *testing.T) {
	identA := generateAgeKey(t)
	identB := generateAgeKey(t)
	envelope := sealForRecipients(t, identA, `{"key_id":"x"}`)

	outPath := filepath.Join(t.TempDir(), "archive-creds.json")
	err := unsealArchiveCreds(&unsealArchiveCredsFlags{
		ageIdentity: identB,
		in:          envelope,
		out:         outPath,
		force:       false,
	})
	if err == nil {
		t.Fatal("unseal with wrong key should fail, got nil error")
	}
	if _, statErr := os.Stat(outPath); !os.IsNotExist(statErr) {
		t.Fatalf("output file should not exist on wrong-key failure: stat err = %v", statErr)
	}
}

// TestUnsealArchiveCreds_BadJSONShape pins the JSON-shape
// sanity check. A wrong-key-on-bad-json envelope would
// silently ship garbage to S3 on the first PUT; this test
// surfaces the failure at unseal time. A "not even JSON"
// plaintext (operator error — they sealed the wrong file)
// must be rejected.
func TestUnsealArchiveCreds_BadJSONShape(t *testing.T) {
	identPath := generateAgeKey(t)
	// Not JSON at all — the operator accidentally sealed
	// the wrong file.
	envelope := sealForRecipients(t, identPath, "this is not json")

	outPath := filepath.Join(t.TempDir(), "archive-creds.json")
	err := unsealArchiveCreds(&unsealArchiveCredsFlags{
		ageIdentity: identPath,
		in:          envelope,
		out:         outPath,
		force:       false,
	})
	if err == nil {
		t.Fatal("bad-JSON-shape plaintext should fail, got nil error")
	}
	if !strings.Contains(err.Error(), "not a valid archive-creds.json") {
		t.Fatalf("err=%q, want substring 'not a valid archive-creds.json'", err.Error())
	}
	if _, statErr := os.Stat(outPath); !os.IsNotExist(statErr) {
		t.Fatalf("output file should not exist on bad-JSON failure: stat err = %v", statErr)
	}
}

// TestBackupInit_CreatesLayout pins the layout `gregalectl backup
// init` lands: the directory at 0700 and the two doctor-detected
// stub files at 0400 root:root (rclone.conf) + 0400 root:root
// (archive-creds.json). Runs the package-private worker so the
// flag-parse boilerplate doesn't pollute the assertion; mirrors
// TestUnsealRclone_HappyPath's seam strategy.
func TestBackupInit_CreatesLayout(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "storage-box")
	f := &backupInitFlags{
		dir:   dir,
		force: false,
	}
	if err := backupInit(f, io.Discard); err != nil {
		t.Fatalf("backupInit: %v", err)
	}

	// Stub 1: rclone.conf placeholder (0400)
	rclonePath := filepath.Join(dir, "rclone.conf")
	st, err := os.Stat(rclonePath)
	if err != nil {
		t.Fatalf("stat rclone.conf: %v", err)
	}
	if mode := st.Mode().Perm(); mode != 0o400 {
		t.Fatalf("rclone.conf mode: got %#o want 0400", mode)
	}
	got, err := os.ReadFile(rclonePath)
	if err != nil {
		t.Fatalf("read rclone.conf: %v", err)
	}
	if !strings.Contains(string(got), "backup init stub") {
		t.Fatalf("rclone.conf stub body should self-identify for doctor detection; got %q", string(got))
	}

	// Stub 2: archive-creds.json envelope (0400, `{}`)
	archivePath := filepath.Join(dir, "archive-creds.json")
	st, err = os.Stat(archivePath)
	if err != nil {
		t.Fatalf("stat archive-creds.json: %v", err)
	}
	if mode := st.Mode().Perm(); mode != 0o400 {
		t.Fatalf("archive-creds.json mode: got %#o want 0400", mode)
	}
	got, err = os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("read archive-creds.json: %v", err)
	}
	if strings.TrimSpace(string(got)) != "{}" {
		t.Fatalf("archive-creds.json stub: got %q want `{}`", strings.TrimSpace(string(got)))
	}
}

// TestBackupInit_RefuseOverwrite pins the refuse-by-default contract
// for `backup init`. Re-running init silently overwrites a
// populated storage-box would strand every sealed envelope the
// operator has previously scp'd in (the box-age-key on disk is the
// identity the unseal path uses; nuking the stub doesn't change
// that, but silently nuking the dir while an unseal is in flight
// races against a tmpfile rename). --force is the escape hatch.
func TestBackupInit_RefuseOverwrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "storage-box")
	f := &backupInitFlags{dir: dir, force: false}
	if err := backupInit(f, io.Discard); err != nil {
		t.Fatalf("first init: %v", err)
	}

	err := backupInit(f, io.Discard)
	if err == nil {
		t.Fatal("second init without --force should refuse, got nil error")
	}
	if !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("refusal error: got %q, want substring 'refusing to overwrite'", err.Error())
	}

	// --force path lands the layout a second time.
	f.force = true
	if err := backupInit(f, io.Discard); err != nil {
		t.Fatalf("forced re-init: %v", err)
	}
}

// The box-age identity file reaches the unseal commands in several shapes:
// cd-compute writes COMPUTE_BOX_AGE_KEY with a trailing newline, and
// operators create it with age-keygen, which adds comment lines. Parsing the
// raw bytes with ParseX25519Identity rejected both, so a join that supplied
// backup envelopes could never unseal them.
func TestUnsealAcceptsBoxAgeIdentityFileShapes(t *testing.T) {
	ident, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	key := ident.String()
	keygen := "# created: 2026-09-29T19:00:00Z\n# public key: " + ident.Recipient().String() + "\n" + key + "\n"
	for _, tc := range []struct {
		name, file string
		ok         bool
	}{
		{"bare key", key, true},
		{"trailing newline (cd-compute printf)", key + "\n", true},
		{"CRLF", key + "\r\n", true},
		{"age-keygen output", keygen, true},
		{"two identities", key + "\n" + other.String() + "\n", false},
		{"empty", "\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			identPath := filepath.Join(dir, "box-age-key")
			if err := os.WriteFile(identPath, []byte(tc.file), 0o400); err != nil {
				t.Fatal(err)
			}
			var sealed strings.Builder
			w, err := age.Encrypt(&sealed, ident.Recipient())
			if err != nil {
				t.Fatal(err)
			}
			const plaintext = `{"endpoint":"https://storage.googleapis.com","bucket":"b","auth_mode":"gcp_metadata"}` + "\n"
			if _, err := w.Write([]byte(plaintext)); err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			envelope := filepath.Join(dir, "envelope.age")
			if err := os.WriteFile(envelope, []byte(sealed.String()), 0o400); err != nil {
				t.Fatal(err)
			}
			archiveErr := unsealArchiveCreds(&unsealArchiveCredsFlags{ageIdentity: identPath, in: envelope, out: filepath.Join(dir, "archive-creds.json")})
			rcloneErr := unsealRclone(&unsealRcloneFlags{ageIdentity: identPath, in: envelope, out: filepath.Join(dir, "rclone.conf")})
			for name, err := range map[string]error{"unseal-archive-creds": archiveErr, "unseal-rclone": rcloneErr} {
				if (err == nil) != tc.ok {
					t.Errorf("%s: err=%v, want ok=%v", name, err, tc.ok)
				}
			}
			if tc.ok {
				got, err := os.ReadFile(filepath.Join(dir, "archive-creds.json"))
				if err != nil || string(got) != plaintext {
					t.Errorf("archive creds round-trip = %q, %v", got, err)
				}
			}
		})
	}
}
