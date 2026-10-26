// commands_audit_anchor.go — audit log tamper evidence (ADR-20261026163015).
package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/auditanchor"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	auditAnchorSub = "anchor"
	auditVerifySub = "verify"
	// auditHMACKeyFileDefault is where apid keeps the audit HMAC key when
	// FAAS_AUDIT_HMAC_KEY is not set (pkg/apid/apidserver).
	auditHMACKeyFileDefault = "/var/lib/faas/audit-hmac.key"
)

// auditAnchorStore opens the store the anchor commands use; tests replace it.
var auditAnchorStore = func(dsn, dbEnv string) (auditanchor.Store, func(), error) {
	pool, err := openFleetSealPool(dsn, dbEnv)
	if err != nil {
		return nil, nil, err
	}
	return state.NewPgStore(pool), pool.Close, nil
}

type auditAnchorFlags struct {
	dsn, dbEnv, keyFile, metricsFile *string
}

func addAuditAnchorFlags(fs *flag.FlagSet) auditAnchorFlags {
	return auditAnchorFlags{
		dsn:         fs.String("pg-dsn", "", "PostgreSQL DSN (default: $FAAS_PG_DSN or $DATABASE_URL)"),
		dbEnv:       fs.String("db-env", "", "systemd env file containing DATABASE_URL (and FAAS_AUDIT_HMAC_KEY when apid takes it from there)"),
		keyFile:     fs.String("key-file", "", "apid's audit HMAC key file (default: $FAAS_AUDIT_HMAC_KEY_FILE or "+auditHMACKeyFileDefault+")"),
		metricsFile: fs.String("metrics-file", "", "node_exporter textfile to write the outcome to"),
	}
}

// cmdAuditAnchor seals the next window of audit_log, logs the anchor (the
// journal ships it off-host), and re-verifies the anchors of the last
// --verify-since. Exit 0 sealed or nothing to seal, 1 the verification
// found tampering, 3 error, including a chain it refuses to extend.
func cmdAuditAnchor(args []string) int {
	fs := flag.NewFlagSet(auditAnchorSub, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	common := addAuditAnchorFlags(fs)
	lag := fs.Duration("lag", auditanchor.DefaultLag, "how far behind now a window ends")
	verifySince := fs.Duration("verify-since", 48*time.Hour, "re-verify the anchors whose windows end within this; 0 skips")
	acceptKeyChange := fs.Bool("accept-key-change", false, "extend a chain sealed under a lost audit HMAC key (see the runbook)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return 2
	}
	log := slog.New(slog.NewJSONHandler(osStdout, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	store, closeStore, key, err := openAuditAnchor(common)
	if err != nil {
		log.Error("audit anchor failed", "err", err)
		return 3
	}
	defer closeStore()
	anchor, sealed, err := auditanchor.Seal(ctx, store, key, time.Now(), auditanchor.SealOptions{Lag: *lag, AcceptKeyChange: *acceptKeyChange})
	if err != nil {
		log.Error("audit anchor failed", "err", err)
		return 3
	}
	if sealed {
		logAnchor(log, "audit anchor sealed", anchor)
	}
	findings := 0
	if *verifySince > 0 {
		from, ok, err := store.FirstAuditLogAnchorEndingAfter(ctx, time.Now().Add(-*verifySince))
		if err != nil {
			log.Error("audit anchor failed", "err", err)
			return 3
		}
		if ok {
			report, err := auditanchor.Verify(ctx, store, key, from)
			if err != nil {
				log.Error("audit anchor failed", "err", err)
				return 3
			}
			findings = logFindings(log, report)
		}
	}
	head, _, err := store.LatestAuditLogAnchor(ctx)
	if err != nil {
		log.Error("audit anchor failed", "err", err)
		return 3
	}
	if err := writePromTextfile(*common.metricsFile, fmt.Sprintf(
		"# HELP faas_audit_anchor_seq Seq of the newest audit log anchor.\n# TYPE faas_audit_anchor_seq gauge\nfaas_audit_anchor_seq %d\n"+
			"# HELP faas_audit_anchor_recent_findings Anchors of the recent window that failed verification on the last run.\n# TYPE faas_audit_anchor_recent_findings gauge\nfaas_audit_anchor_recent_findings %d\n"+
			"# HELP faas_audit_anchor_last_success_timestamp_seconds Unix time of the last audit anchor run that completed.\n# TYPE faas_audit_anchor_last_success_timestamp_seconds gauge\nfaas_audit_anchor_last_success_timestamp_seconds %d\n",
		head.Seq, findings, time.Now().Unix())); err != nil {
		log.Error("audit anchor metrics", "err", err)
		return 3
	}
	if findings > 0 {
		return 1
	}
	return 0
}

// cmdAuditVerify recomputes the anchor chain from --from-seq (all of it by
// default) and prints the report. Exit 0 clean, 1 findings, 3 error.
func cmdAuditVerify(args []string) int {
	fs := flag.NewFlagSet(auditVerifySub, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	common := addAuditAnchorFlags(fs)
	fromSeq := fs.Int64("from-seq", 1, "first anchor to verify")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return 2
	}
	log := slog.New(slog.NewJSONHandler(osStderr, nil))
	store, closeStore, key, err := openAuditAnchor(common)
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl audit verify:", err)
		return 3
	}
	defer closeStore()
	report, err := auditanchor.Verify(context.Background(), store, key, *fromSeq)
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl audit verify:", err)
		return 3
	}
	logFindings(log, report)
	if err := writePromTextfile(*common.metricsFile, fmt.Sprintf(
		"# HELP faas_audit_verify_findings Anchors that failed the last full audit log verification.\n# TYPE faas_audit_verify_findings gauge\nfaas_audit_verify_findings %d\n"+
			"# HELP faas_audit_verify_last_success_timestamp_seconds Unix time of the last full audit log verification that completed.\n# TYPE faas_audit_verify_last_success_timestamp_seconds gauge\nfaas_audit_verify_last_success_timestamp_seconds %d\n",
		len(report.Findings), time.Now().Unix())); err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl audit verify:", err)
		return 3
	}
	if jsonEnabled() {
		if code := emitOperatorJSON(report); code != 0 {
			return code
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "verified anchors %d to %d (%d anchors, %d rows); newest mac %s\n",
			report.FromSeq, report.LastSeq, report.Anchors, report.Rows, report.LastMAC)
		for _, f := range report.Findings {
			_, _ = fmt.Fprintf(osStdout, "anchor %d %s: %s\n", f.Seq, f.Kind, f.Detail)
		}
	}
	if len(report.Findings) > 0 {
		return 1
	}
	return 0
}

func openAuditAnchor(f auditAnchorFlags) (auditanchor.Store, func(), []byte, error) {
	auditKey, err := loadAuditHMACKey(*f.dbEnv, *f.keyFile, os.Getenv)
	if err != nil {
		return nil, nil, nil, err
	}
	key, err := auditanchor.DeriveKey(auditKey)
	if err != nil {
		return nil, nil, nil, err
	}
	store, closeStore, err := auditAnchorStore(*f.dsn, *f.dbEnv)
	if err != nil {
		return nil, nil, nil, err
	}
	return store, closeStore, key, nil
}

// loadAuditHMACKey finds apid's audit HMAC key as apid does:
// FAAS_AUDIT_HMAC_KEY (from the environment, then the --db-env file), then
// the key file. It never generates one.
func loadAuditHMACKey(dbEnv, keyFile string, getenv func(string) string) ([]byte, error) {
	hexKey, source := getenv("FAAS_AUDIT_HMAC_KEY"), "FAAS_AUDIT_HMAC_KEY"
	if hexKey == "" && dbEnv != "" {
		hexKey, _ = readEnvFileValue(dbEnv, "FAAS_AUDIT_HMAC_KEY")
		source = dbEnv
	}
	if hexKey == "" {
		source = keyFile
		if source == "" {
			source = getenv("FAAS_AUDIT_HMAC_KEY_FILE")
		}
		if source == "" {
			source = auditHMACKeyFileDefault
		}
		//nolint:forbidigo // operator-chosen key file on the control plane.
		data, err := os.ReadFile(source)
		if err != nil {
			return nil, fmt.Errorf("read the audit HMAC key: %w", err)
		}
		hexKey = string(data)
	}
	key, err := hex.DecodeString(strings.TrimSpace(hexKey))
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("the audit HMAC key from %s is not 32 hex-encoded bytes", source)
	}
	return key, nil
}

func logAnchor(log *slog.Logger, msg string, a state.AuditLogAnchor) {
	log.Info(msg, "seq", a.Seq, "window_start", a.WindowStart.Format(time.RFC3339), "window_end", a.WindowEnd.Format(time.RFC3339),
		"rows", a.RowCount, "rows_digest", a.RowsDigest, "prev_mac", a.PrevMAC, "mac", a.MAC, "key_id", a.KeyID)
}

func logFindings(log *slog.Logger, report auditanchor.Report) int {
	for _, f := range report.Findings {
		log.Warn("audit anchor finding", "seq", f.Seq, "kind", f.Kind, "detail", f.Detail)
	}
	return len(report.Findings)
}

// writePromTextfile replaces a node_exporter textfile atomically; "" skips.
func writePromTextfile(path, body string) error {
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".audit-anchor-prom-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, werr := tmp.WriteString(body)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr, os.Chmod(tmp.Name(), 0o644)); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
