//go:build linux || darwin

package runtimequota

// adr: 632

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

func provisionTestKeys(t *testing.T, role, issuer string) KeyConfig {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "keys")
	if _, err := ProvisionAuthorityKeys(dir, role, issuer); err != nil {
		t.Fatal(err)
	}
	return KeyConfig{PrivateKeyPath: filepath.Join(dir, PrivateKeyFilename), TrustBundlePath: filepath.Join(dir, TrustFilename)}
}

func TestAuthorityKeysStableRestartAndRotation(t *testing.T) {
	g, _, _, _ := fixture(t)
	cfg := provisionTestKeys(t, Directory, g.IssuerID)
	old, err := LoadAuthorityKeys(cfg, Directory, g.IssuerID)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := LoadAuthorityKeys(cfg, Directory, g.IssuerID)
	if err != nil || restarted.Signer.KeyID() != old.Signer.KeyID() {
		t.Fatal("signer changed on restart", err)
	}
	e, err := old.Signer.SignGrant(g)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := restarted.Trust.VerifyGrant(e, g.Binding); err != nil || got != g {
		t.Fatal("restart lost authority", err)
	}
	nextCfg := provisionTestKeys(t, Directory, g.IssuerID)
	next, err := LoadAuthorityKeys(nextCfg, Directory, g.IssuerID)
	if err != nil || next.Signer.KeyID() == old.Signer.KeyID() {
		t.Fatal("rotation did not create an independent key", err)
	}
	anchors, _ := LoadTrustAnchors(cfg.TrustBundlePath)
	nextAnchors, _ := LoadTrustAnchors(nextCfg.TrustBundlePath)
	bundle := filepath.Join(t.TempDir(), "overlap.json")
	if err := WriteTrustBundle(bundle, append(anchors, nextAnchors...)); err != nil {
		t.Fatal(err)
	}
	nextCfg.TrustBundlePath = bundle
	next, err = LoadAuthorityKeys(nextCfg, Directory, g.IssuerID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := next.Trust.VerifyGrant(e, g.Binding); err != nil {
		t.Fatal("overlap lost the historical key", err)
	}
	if err := WriteTrustBundle(bundle, anchors); err == nil {
		t.Fatal("overwrote a published bundle")
	}
	before, _ := os.ReadFile(cfg.PrivateKeyPath)
	if _, err := ProvisionAuthorityKeys(filepath.Dir(cfg.PrivateKeyPath), Directory, g.IssuerID); err == nil {
		t.Fatal("overwrote an existing key directory")
	}
	after, _ := os.ReadFile(cfg.PrivateKeyPath)
	if !bytes.Equal(before, after) {
		t.Fatal("existing key changed")
	}
}

func TestAuthorityKeyIdentityAndRolePins(t *testing.T) {
	issuer := uuid.NewString()
	cfg := provisionTestKeys(t, Cell, issuer)
	for _, tc := range []struct {
		name, role, issuer string
		config             KeyConfig
	}{
		{"wrong-role", Directory, issuer, cfg},
		{"wrong-cell", Cell, uuid.NewString(), cfg},
		{"noncanonical-cell", Cell, "AAAAAAAA-AAAA-4AAA-AAAA-AAAAAAAAAAAA", cfg},
		{"missing-private", Cell, issuer, KeyConfig{TrustBundlePath: cfg.TrustBundlePath}},
		{"missing-trust", Cell, issuer, KeyConfig{PrivateKeyPath: cfg.PrivateKeyPath}},
		{"empty", Cell, issuer, KeyConfig{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := LoadAuthorityKeys(tc.config, tc.role, tc.issuer); err == nil {
				t.Fatal("accepted mismatched or incomplete configuration")
			}
		})
	}
	other := provisionTestKeys(t, Cell, issuer)
	cfg.TrustBundlePath = other.TrustBundlePath
	if _, err := LoadAuthorityKeys(cfg, Cell, issuer); !errors.Is(err, ErrTrust) {
		t.Fatal("unanchored private key accepted", err)
	}
}

func TestAuthorityKeyFilesRejectUnsafeMaterial(t *testing.T) {
	for _, change := range []string{"private-writable", "private-world", "trust-writable", "symlink", "hardlink", "fifo", "oversized", "leading-junk", "second-pem", "malformed-pem"} {
		t.Run(change, func(t *testing.T) {
			issuer := uuid.NewString()
			cfg := provisionTestKeys(t, Cell, issuer)
			path := cfg.PrivateKeyPath
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "private-writable":
				err = os.Chmod(path, 0o600)
			case "private-world":
				err = os.Chmod(path, 0o444)
			case "trust-writable":
				err = os.Chmod(cfg.TrustBundlePath, 0o644)
			case "symlink", "hardlink", "fifo":
				moved := path + ".retained"
				if err = os.Rename(path, moved); err != nil {
					t.Fatal(err)
				}
				if change == "symlink" {
					err = os.Symlink(moved, path)
				} else if change == "hardlink" {
					err = os.Link(moved, path)
				} else {
					err = unix.Mkfifo(path, 0o400)
				}
			default:
				if err = os.Chmod(path, 0o600); err != nil {
					t.Fatal(err)
				}
				switch change {
				case "oversized":
					raw = bytes.Repeat([]byte("x"), api.RegionalRuntimeSigningKeyMaxBytes+1)
				case "leading-junk":
					raw = append([]byte("secret-looking invalid prefix\n"), raw...)
				case "second-pem":
					raw = append(raw, raw...)
				case "malformed-pem":
					raw = []byte("not a private key")
				}
				if err = os.WriteFile(path, raw, 0o600); err != nil {
					t.Fatal(err)
				}
				err = os.Chmod(path, 0o400)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = LoadAuthorityKeys(cfg, Cell, issuer); err == nil {
				t.Fatal("accepted unsafe signing material")
			}
		})
	}
}

func TestQuotaTrustBundleStrictAndBounded(t *testing.T) {
	issuer := uuid.NewString()
	cfg := provisionTestKeys(t, Cell, issuer)
	raw, err := os.ReadFile(cfg.TrustBundlePath)
	if err != nil {
		t.Fatal(err)
	}
	var bundle trustBundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []string{"duplicate-field", "unknown-field", "whitespace", "duplicate-key", "wrong-fingerprint", "wrong-role", "oversized", "too-many-anchors"} {
		t.Run(tc, func(t *testing.T) {
			changed := bundle
			changed.Anchors = append([]publicAnchor(nil), bundle.Anchors...)
			input := append([]byte(nil), raw...)
			switch tc {
			case "duplicate-field":
				input = bytes.Replace(input, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1)
			case "unknown-field":
				input = bytes.Replace(input, []byte(`"version":1`), []byte(`"unknown":1,"version":1`), 1)
			case "whitespace":
				input = append([]byte(" "), input...)
			case "oversized":
				input = bytes.Repeat([]byte("x"), api.RegionalRuntimeTrustBundleMaxBytes+1)
			default:
				switch tc {
				case "duplicate-key":
					changed.Anchors = append(changed.Anchors, changed.Anchors[0])
				case "wrong-fingerprint":
					changed.Anchors[0].KeyID = strings.Repeat("0", 64)
				case "wrong-role":
					changed.Anchors[0].Role = "vmmd"
				case "too-many-anchors":
					changed.Anchors = make([]publicAnchor, api.RegionalRuntimeTrustAnchorsMax+1)
				}
				input, _ = json.Marshal(changed)
			}
			if _, err := decodeTrustBundle(input); err == nil {
				t.Fatal("accepted ambiguous or oversized trust bundle")
			}
		})
	}
}
