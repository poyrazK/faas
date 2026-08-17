// Tests for warnIfPkiCNMismatch (issue #900 follow-up).
//
// The Warn fires when the loaded TLS leaf cert's CN does not match
// the registered-set name that registerComputeNode will write. The
// compare uses the same `.faas`-append rule that registerComputeNode
// applies. Tests pin:
//   - empty NodeName (single-box dev path) → no Warn
//   - nil serverTLS (unix socket path) → no Warn
//   - leaf CN matches the expected registered name → no Warn
//   - leaf CN does NOT match the expected registered name → Warn
//   - empty CN on the leaf → Warn (different message; CN-empty is
//     itself a tamper signal — see pkg/wire/node_verifier.go:99)
//
// Each test captures the slog records via a JSON handler so the
// assertion navigates structured attributes rather than scraping
// text. This is the same pattern command_doctor_test.go uses for
// doctor findings.

package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"log/slog"
	"math/big"
	"strings"
	"testing"
	"time"
)

// loggerCapture buffers slog JSON records written through a *slog.Logger
// so tests can assert on individual records by message substring. The
// findRecords method below is what the assertions call.
type loggerCapture struct {
	out *bytes.Buffer
}

func newLoggerCapture() (*slog.Logger, *loggerCapture) {
	cap := &loggerCapture{out: &bytes.Buffer{}}
	h := slog.NewJSONHandler(cap.out, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(h)
	return logger, cap
}

// findRecords returns the captured records that match the given
// message substring. A nil return means the substring wasn't seen.
func (c *loggerCapture) findRecords(substr string) []map[string]any {
	dec := json.NewDecoder(bytes.NewReader(c.out.Bytes()))
	var out []map[string]any
	for dec.More() {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			break
		}
		if msg, ok := m["msg"].(string); ok && strings.Contains(msg, substr) {
			out = append(out, m)
		}
	}
	return out
}

// makeLeafTLSConfig generates a self-signed cert with the given CN
// and wraps it in a *tls.Config suitable for the warnIfPkiCNMismatch
// entry point. The cert is not chained to a CA — the helper only
// needs the leaf-CN-bearing DER blob.
func makeLeafTLSConfig(t *testing.T, cn string) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("x509.CreateCertificate: %v", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{
			{Certificate: [][]byte{der}, PrivateKey: key},
		},
	}
}

// TestWarnIfPkiCNMismatch_EmptyNameSilent: the single-box dev path.
// The operator left [compute_node].name empty; vmmd won't
// self-register, so the registered-set side has no row to compare
// against. The Warn is silent.
func TestWarnIfPkiCNMismatch_EmptyNameSilent(t *testing.T) {
	logger, cap := newLoggerCapture()
	cfg := ComputeNodeConfig{NodeName: ""} // operator opted out
	serverTLS := makeLeafTLSConfig(t, "vmmd-1.faas")
	warnIfPkiCNMismatch(cfg, serverTLS, logger)
	if records := cap.findRecords("pki leaf CN"); len(records) > 0 {
		t.Errorf("empty Name should not trigger pki-mismatch Warn; got %d records", len(records))
	}
}

// TestWarnIfPkiCNMismatch_NilServerTLSSilent: the unix-socket path.
// No TLS is loaded at all; the verifier isn't installed. The Warn
// is silent — there's no leaf CN to compare against.
func TestWarnIfPkiCNMismatch_NilServerTLSSilent(t *testing.T) {
	logger, cap := newLoggerCapture()
	cfg := ComputeNodeConfig{NodeName: "gcp-faas-node-1"}
	warnIfPkiCNMismatch(cfg, nil, logger)
	if records := cap.findRecords("pki leaf CN"); len(records) > 0 {
		t.Errorf("nil serverTLS should not trigger pki-mismatch Warn; got %d records", len(records))
	}
}

// TestWarnIfPkiCNMismatch_EmptyCertSilent: the legacy / pre-TLS
// boot path. The leaf has an empty CN — the call logs a different
// Warn (the tamper-signal one) but not the "mismatch" Warn.
func TestWarnIfPkiCNMismatch_EmptyCertSilent(t *testing.T) {
	logger, cap := newLoggerCapture()
	cfg := ComputeNodeConfig{NodeName: "gcp-faas-node-1"}
	serverTLS := makeLeafTLSConfig(t, "")
	warnIfPkiCNMismatch(cfg, serverTLS, logger)
	if records := cap.findRecords("pki leaf CN does not match"); len(records) > 0 {
		t.Errorf("empty-CN leaf should not trigger mismatch Warn; got %d records", len(records))
	}
	if records := cap.findRecords("leaf cert has empty CN"); len(records) == 0 {
		t.Errorf("empty-CN leaf should trigger the empty-CN Warn; got 0 records")
	}
}

// TestWarnIfPkiCNMismatch_Match: the operator's CN already ends in
// `.faas` AND matches the expected name. The Warn is silent.
func TestWarnIfPkiCNMismatch_Match(t *testing.T) {
	logger, cap := newLoggerCapture()
	cfg := ComputeNodeConfig{NodeName: "gcp-faas-node-1"}
	serverTLS := makeLeafTLSConfig(t, "gcp-faas-node-1.faas")
	warnIfPkiCNMismatch(cfg, serverTLS, logger)
	if records := cap.findRecords("pki leaf CN does not match"); len(records) > 0 {
		t.Errorf("matching leaf CN should not trigger Warn; got %d records", len(records))
	}
}

// TestWarnIfPkiCNMismatch_MatchWithSuffix: the operator's CN
// matches the rewritten form (the `.faas` is already there, AND
// the operator's TOML name lacks the suffix). The Warn is silent
// because the registered-set rewrite produces the same string.
func TestWarnIfPkiCNMismatch_MatchWithSuffix(t *testing.T) {
	logger, cap := newLoggerCapture()
	cfg := ComputeNodeConfig{NodeName: "gcp-faas-node-1"}     // no .faas in TOML
	serverTLS := makeLeafTLSConfig(t, "gcp-faas-node-1.faas") // leaf already has it
	warnIfPkiCNMismatch(cfg, serverTLS, logger)
	if records := cap.findRecords("pki leaf CN does not match"); len(records) > 0 {
		t.Errorf("leaf CN with .faas should match post-rewrite row name; got %d records", len(records))
	}
}

// TestWarnIfPkiCNMismatch_Mismatch: the canonical case. The
// operator's leaf CN is the TOML value verbatim (no .faas); the
// registered-set side will auto-append .faas. The Warn fires.
func TestWarnIfPkiCNMismatch_Mismatch(t *testing.T) {
	logger, cap := newLoggerCapture()
	cfg := ComputeNodeConfig{NodeName: "gcp-faas-node-1"}
	serverTLS := makeLeafTLSConfig(t, "gcp-faas-node-1") // no .faas
	warnIfPkiCNMismatch(cfg, serverTLS, logger)
	records := cap.findRecords("pki leaf CN does not match")
	if len(records) != 1 {
		t.Fatalf("expected 1 mismatch Warn, got %d", len(records))
	}
	r := records[0]
	// The Warn must name BOTH sides so the operator can grep-jump
	// to the fix (see LeaveCN / ConfigName / RegisteredName attrs).
	leafCN, ok := r["leaf_cn"].(string)
	if !ok || leafCN != "gcp-faas-node-1" {
		t.Errorf("leaf_cn = %v, want %q", r["leaf_cn"], "gcp-faas-node-1")
	}
	configName, ok := r["config_name"].(string)
	if !ok || configName != "gcp-faas-node-1" {
		t.Errorf("config_name = %v, want %q", r["config_name"], "gcp-faas-node-1")
	}
	registeredName, ok := r["registered_name"].(string)
	if !ok || registeredName != "gcp-faas-node-1.faas" {
		t.Errorf("registered_name = %v, want %q", r["registered_name"], "gcp-faas-node-1.faas")
	}
	// The fix line should hint at the operator's next step.
	fix, ok := r["fix"].(string)
	if !ok || !strings.Contains(fix, "gregale pki init") {
		t.Errorf("fix = %v, want substring %q", r["fix"], "gregale pki init")
	}
}

// TestLeafCNFromTLSConfig: unit-level pin on the leaf-CN reader.
// Empty inputs (nil cfg, no Certificates, no DER) return ("", nil);
// parse errors surface verbatim.
func TestLeafCNFromTLSConfig(t *testing.T) {
	t.Run("nil cfg", func(t *testing.T) {
		cn, err := leafCNFromTLSConfig(nil)
		if err != nil || cn != "" {
			t.Errorf("nil cfg: got (%q, %v), want (\"\", nil)", cn, err)
		}
	})
	t.Run("no certificates", func(t *testing.T) {
		cn, err := leafCNFromTLSConfig(&tls.Config{})
		if err != nil || cn != "" {
			t.Errorf("no certs: got (%q, %v), want (\"\", nil)", cn, err)
		}
	})
	t.Run("empty der blob", func(t *testing.T) {
		cn, err := leafCNFromTLSConfig(&tls.Config{
			Certificates: []tls.Certificate{{Certificate: nil}},
		})
		if err != nil || cn != "" {
			t.Errorf("empty DER: got (%q, %v), want (\"\", nil)", cn, err)
		}
	})
	t.Run("valid cert", func(t *testing.T) {
		cfg := makeLeafTLSConfig(t, "vmmd-east-1.faas")
		cn, err := leafCNFromTLSConfig(cfg)
		if err != nil {
			t.Fatalf("leafCNFromTLSConfig: %v", err)
		}
		if cn != "vmmd-east-1.faas" {
			t.Errorf("cn = %q, want %q", cn, "vmmd-east-1.faas")
		}
	})
}

// _ keeps imports referenced when the file is read in isolation.
var _ = context.Background
