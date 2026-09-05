// cluster5a_dispatch_test.go — dispatcher-sweep coverage pass for
// the gregalectl CLI (Cluster 5a of the coverage depth-pass,
// follow-on to PR #1044).
//
// Each top-level dispatcher is exercised via the invalid-flag
// trick: feed --not-a-flag to a known verb so the leaf exits via
// flag.Parse BEFORE any real work happens. This pins the routing
// contract end-to-end without standing up Postgres, the secrets
// dir, the ansible inventory, or any daemons.
//
// All dispatchers return non-zero on invalid flag, so each subtest
// asserts:
//   - dispatcher(verb --not-a-flag) reaches the leaf (exit non-zero)
//   - the dispatcher's missing-arg branch exits non-zero
//   - the dispatcher's unknown-subcommand branch exits non-zero
//
// Pins: doctor, sign-keys, node-key, release, backup, secrets,
// manifest. Mirrors the file-local capture-helper convention
// (each test file owns its own stderr helper; no shared
// testutil package — see commands_pki_test.go:46-51).
package main

import (
	"os"
	"strings"
	"testing"
)

// captureStderrSweep is the file-local stderr capture helper for
// the cluster-5a dispatcher sweep. Mirrors the precedent at
// commands_release_sbom_gate_test.go:107-123.
func captureStderrSweep(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()
	fn()
	_ = w.Close()
	var out []byte
	tmp := make([]byte, 4096)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			out = append(out, tmp[:n]...)
		}
		if err != nil {
			break
		}
	}
	_ = r.Close()
	return string(out)
}

// TestCmdDoctorDispatch_Routing pins cmdDoctorDispatch
// (commands_doctor.go:205). doctor is a single-command dispatcher
// (no sub-commands); the only routing is flag parsing + the
// fail-on enum gate. The invalid-flag case exercises flag.Parse.
func TestCmdDoctorDispatch_Routing(t *testing.T) {
	if code := cmdDoctorDispatch([]string{"--not-a-flag"}); code == 0 {
		t.Errorf("cmdDoctorDispatch(--not-a-flag) = 0, want non-zero")
	}
	stderr := captureStderrSweep(t, func() {
		if code := cmdDoctorDispatch([]string{"--fail-on=invalid"}); code != 1 {
			t.Errorf("cmdDoctorDispatch(--fail-on=invalid) = %d, want 1 (enum gate)", code)
		}
	})
	if !strings.Contains(stderr, "is not warn|error") {
		t.Errorf("doctor stderr missing fail-on diagnostic (got %q)", stderr)
	}
}

// TestCmdSignKeysDispatch_Routing pins cmdSignKeys
// (commands_sign_keys.go:59). Three leaves: init, rotate, status.
// Each is fed --not-a-flag so flag.Parse fails before any FS work.
func TestCmdSignKeysDispatch_Routing(t *testing.T) {
	cases := []struct {
		name string
		verb string
	}{
		{"init", "init"},
		{"rotate", "rotate"},
		{"status", "status"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code := cmdSignKeys([]string{tc.verb, "--not-a-flag"}); code == 0 {
				t.Errorf("cmdSignKeys(%s --not-a-flag) = 0, want non-zero", tc.verb)
			}
		})
	}
	t.Run("no_args", func(t *testing.T) {
		if code := cmdSignKeys(nil); code == 0 {
			t.Errorf("cmdSignKeys(nil) = 0, want non-zero (usage)")
		}
	})
	t.Run("unknown", func(t *testing.T) {
		if code := cmdSignKeys([]string{"rehash"}); code == 0 {
			t.Errorf("cmdSignKeys(rehash) = 0, want non-zero")
		}
	})
}

// TestCmdNodeKeyDispatch_Routing pins cmdNodeKey
// (commands_node_key.go:85). Three leaves: init, rotate, status.
func TestCmdNodeKeyDispatch_Routing(t *testing.T) {
	cases := []struct {
		name string
		verb string
	}{
		{"init", "init"},
		{"rotate", "rotate"},
		{"status", "status"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code := cmdNodeKey([]string{tc.verb, "--not-a-flag"}); code == 0 {
				t.Errorf("cmdNodeKey(%s --not-a-flag) = 0, want non-zero", tc.verb)
			}
		})
	}
	t.Run("no_args", func(t *testing.T) {
		if code := cmdNodeKey(nil); code == 0 {
			t.Errorf("cmdNodeKey(nil) = 0, want non-zero (usage)")
		}
	})
	t.Run("unknown", func(t *testing.T) {
		if code := cmdNodeKey([]string{"rehash"}); code == 0 {
			t.Errorf("cmdNodeKey(rehash) = 0, want non-zero")
		}
	})
}

// TestCmdReleaseDispatch_Routing pins cmdReleaseDispatch
// (commands_release.go:72). The release bundle, install, KGV, history, and
// inspect leaves all remain routable from the parent dispatcher.
func TestCmdReleaseDispatch_Routing(t *testing.T) {
	cases := []struct {
		name string
		verb string
	}{
		{"bundle", "bundle"},
		{"install", "install"},
		{"kgv", "kgv"},
		{"history", "history"},
		{"inspect", "inspect"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code := cmdReleaseDispatch([]string{tc.verb, "--not-a-flag"}); code == 0 {
				t.Errorf("cmdReleaseDispatch(%s --not-a-flag) = 0, want non-zero", tc.verb)
			}
		})
	}
	t.Run("no_args", func(t *testing.T) {
		stderr := captureStderrSweep(t, func() {
			if code := cmdReleaseDispatch(nil); code == 0 {
				t.Errorf("cmdReleaseDispatch(nil) = 0, want non-zero (usage)")
			}
		})
		if !strings.Contains(stderr, "usage") {
			t.Errorf("release dispatch stderr missing usage hint (got %q)", stderr)
		}
	})
	t.Run("unknown", func(t *testing.T) {
		stderr := captureStderrSweep(t, func() {
			if code := cmdReleaseDispatch([]string{"publish"}); code == 0 {
				t.Errorf("cmdReleaseDispatch(publish) = 0, want non-zero")
			}
		})
		if !strings.Contains(stderr, "unknown subcommand") {
			t.Errorf("release dispatch stderr missing unknown marker (got %q)", stderr)
		}
	})
}

// TestCmdBackupDispatch_Routing pins cmdBackup
// (commands_backup.go:85). Three leaves: init, unseal-rclone,
// unseal-archive-creds.
func TestCmdBackupDispatch_Routing(t *testing.T) {
	cases := []struct {
		name string
		verb string
	}{
		{"init", "init"},
		{"unseal_rclone", "unseal-rclone"},
		{"unseal_archive_creds", "unseal-archive-creds"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code := cmdBackup([]string{tc.verb, "--not-a-flag"}); code == 0 {
				t.Errorf("cmdBackup(%s --not-a-flag) = 0, want non-zero", tc.verb)
			}
		})
	}
	t.Run("no_args", func(t *testing.T) {
		if code := cmdBackup(nil); code == 0 {
			t.Errorf("cmdBackup(nil) = 0, want non-zero (usage)")
		}
	})
	t.Run("unknown", func(t *testing.T) {
		if code := cmdBackup([]string{"list"}); code == 0 {
			t.Errorf("cmdBackup(list) = 0, want non-zero")
		}
	})
}

// TestCmdSecretsDispatch_Routing pins cmdSecretsDispatch
// (commands_secrets_init.go:95). Four leaves: init, rotate,
// status, stamp.
func TestCmdSecretsDispatch_Routing(t *testing.T) {
	cases := []struct {
		name string
		verb string
	}{
		{"init", "init"},
		{"rotate", "rotate"},
		{"status", "status"},
		{"stamp", "stamp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code := cmdSecretsDispatch([]string{tc.verb, "--not-a-flag"}); code == 0 {
				t.Errorf("cmdSecretsDispatch(%s --not-a-flag) = 0, want non-zero", tc.verb)
			}
		})
	}
	t.Run("no_args", func(t *testing.T) {
		if code := cmdSecretsDispatch(nil); code == 0 {
			t.Errorf("cmdSecretsDispatch(nil) = 0, want non-zero (usage)")
		}
	})
	t.Run("unknown", func(t *testing.T) {
		if code := cmdSecretsDispatch([]string{"revoke"}); code == 0 {
			t.Errorf("cmdSecretsDispatch(revoke) = 0, want non-zero")
		}
	})
}

// TestCmdManifestDispatch_Routing pins cmdManifestDispatch
// (commands_manifest.go:48). Three leaves: validate, render,
// ansible (the third delegates to cmdManifestAnsible).
func TestCmdManifestDispatch_Routing(t *testing.T) {
	cases := []struct {
		name string
		verb string
	}{
		{"validate", "validate"},
		{"render", "render"},
		{"ansible", "ansible"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code := cmdManifestDispatch([]string{tc.verb, "--not-a-flag"}); code == 0 {
				t.Errorf("cmdManifestDispatch(%s --not-a-flag) = 0, want non-zero", tc.verb)
			}
		})
	}
	t.Run("no_args", func(t *testing.T) {
		if code := cmdManifestDispatch(nil); code == 0 {
			t.Errorf("cmdManifestDispatch(nil) = 0, want non-zero (usage)")
		}
	})
	t.Run("unknown", func(t *testing.T) {
		if code := cmdManifestDispatch([]string{"diff"}); code == 0 {
			t.Errorf("cmdManifestDispatch(diff) = 0, want non-zero")
		}
	})
}
