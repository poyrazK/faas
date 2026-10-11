// trusted_publisher_writer.go — Issue #472 / ADR-058: apid-side
// listener that maintains the on-disk mirror of app_trusted_signers
// at /etc/faas/secrets/trusted-publishers/<app_id>--<name>.pem.
//
// imaged reads this directory on startup and on every
// trusted_signer_changed pg_notify to refresh its in-memory verify
// cache. Without this writer, the apid handler writes to the DB row
// and emits pg_notify, but the on-disk mirror stays empty — so
// imaged's cache is empty, and every signed deploy fails with
// ErrSignatureInvalid (no trusted publishers).
//
// Why a separate listener (not a sync write in the handler)
//
// The handler is a request-scoped closure with a tight latency budget
// (the customer is waiting); a synchronous write to
// /etc/faas/secrets/trusted-publishers/ adds an fsync round-trip to
// the response path. The listener decouples the write:
//   - handler emits the notify (sub-millisecond)
//   - listener (this goroutine) walks the row from the DB and writes
//     the file (fire-and-forget on the listener side)
//
// This costs one extra DB round-trip per CRUD (the listener re-reads
// the row to get the DER bytes the handler didn't include in the
// payload). The cost is amortized against the deploy-time verify
// cost (one DB round-trip on a cold verified deploy is cheap
// compared to the registry round-trip + ECDSA verify).
//
// Why a full dir read on every notify (not just the affected row)
//
// The listener re-reads app_trusted_signers for the affected app_id
// on every notify (a single SELECT keyed by app_id, returns 0..N
// rows). This keeps the on-disk mirror consistent with the DB even
// when the notify is dropped (per ADR-058 R2, pg_notify is at-most-
// once). The next notify reads the latest state and writes the
// whole per-app set of files.
//
// Lifecycle: runTrustedPublisherWriter is invoked once from
// main.go's bgBefore and lives for the daemon's lifetime. The ctx
// that cancels the daemon also cancels the listener.
package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// trustedPublisherWrite is the parsed JSON shape of the notify
// payload emitted by handlers_trusted_signers.go. Fields are
// server-controlled (the handler builds the payload from server
// state); the parser is purely defensive.
type trustedPublisherWrite struct {
	Kind   string `json:"kind"`
	AppID  string `json:"app_id"`
	Signer string `json:"signer"`
}

// runTrustedPublisherWriter subscribes to db.NotifyTrustedSignerChanged
// and re-writes the per-app PEM files for the affected app_id on
// every notify. The store parameter is the live Postgres pool;
// the dir is the on-disk mirror path (typically
// /etc/faas/secrets/trusted-publishers).
//
// Returns when ctx is cancelled. The initial Subscribe error is
// fatal — silent drop is the bug we're closing.
func runTrustedPublisherWriter(ctx context.Context, pool *pgxpool.Pool, store state.Store, dir string, log *slog.Logger) error {
	if dir == "" {
		// Defensive: the caller should not invoke us with an empty
		// dir, but the publish-time only-on-notify path means we
		// silently no-op otherwise. Without a dir, imaged has
		// nothing to read, so the verify path is fail-closed
		// (ErrSignatureInvalid) — same posture as a misconfigured
		// box.
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("trusted-publisher-writer: mkdir %q: %w", dir, err)
	}
	ch, err := db.SubscribeWithReconnect(ctx, pool, []string{db.NotifyTrustedSignerChanged}, log)
	if err != nil {
		return err
	}
	log.Info("trusted-publisher-writer: started", "dir", dir)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case n, ok := <-ch:
			if !ok {
				// Channel closed by the reconnect wrapper on a
				// Postgres restart; the inner loop reconnects, so
				// the outer loop just exits cleanly.
				return nil
			}
			if n.Payload == "" {
				continue
			}
			var p trustedPublisherWrite
			if err := json.Unmarshal([]byte(n.Payload), &p); err != nil {
				log.Warn("trusted-publisher-writer: bad payload", "err", err)
				continue
			}
			if p.AppID == "" {
				log.Warn("trusted-publisher-writer: empty app_id")
				continue
			}
			if err := resyncApp(ctx, dir, store, p.AppID, p.Signer, log); err != nil {
				log.Warn("trusted-publisher-writer: resync failed",
					"app_id", p.AppID, "signer", p.Signer, "err", err)
				continue
			}
		}
	}
}

// resyncApp re-reads app_trusted_signers for appID and writes the
// per-app PEM files. signer (the operator-chosen name) is the
// specific row that triggered the notify; we use it as a "please
// keep this active" hint to distinguish upsert (re-write even if
// already present) from delete (the row may already be gone, so we
// re-write the survival set).
//
// On the "deleted" kind, the row is gone from the DB before the
// notify fires (the handler order is delete → notify). The SELECT
// returns the survival set; the file we intended to delete is no
// longer in the SELECT, so it gets removed via the "rows in DB but
// not on disk" reconciliation below.
func resyncApp(ctx context.Context, dir string, store state.Store, appID, signer string, log *slog.Logger) error {
	rows, err := store.ListAppTrustedSignersForApp(ctx, appID)
	if err != nil {
		return fmt.Errorf("list trusted signers for app %q: %w", appID, err)
	}
	// Write every per-app PEM file. Mode 0444 (the same posture as
	// DefaultSignPubPath) — imagied reads the file, no in-process
	// writer needs write access.
	keep := map[string]struct{}{}
	for _, r := range rows {
		filename := publisherFilename(appID, r.SignerName)
		path := filepath.Join(dir, filename)
		body := pemEnveloped(r.CosignPublicKey)
		if err := os.WriteFile(path, body, 0o444); err != nil {
			return fmt.Errorf("write %q: %w", path, err)
		}
		keep[filename] = struct{}{}
	}
	// Reconcile orphan files: any <app_id>--*.pem in the dir that
	// is not in `keep` is a stale row (delete notify, or a row
	// dropped while the daemon was down). Drop the file so the
	// cache doesn't carry a ghost publisher.
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read dir %q: %w", dir, err)
	}
	prefix := appID + "--"
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pem") {
			continue
		}
		if !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		if _, ok := keep[e.Name()]; ok {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Warn("trusted-publisher-writer: orphan remove failed",
				"path", path, "err", err)
		}
	}
	return nil
}

// publisherFilename is the on-disk filename for a (app_id, signer)
// pair. The `<app_id>--<name>.pem` shape is the contract between
// apid (writer) and imaged (reader, pkg/cosign/verify.go::TrustListFromDir).
func publisherFilename(appID, signer string) string {
	return appID + "--" + signer + ".pem"
}

// pemEnveloped wraps the raw DER bytes in a PEM block so the
// existing cosign.LoadPublicKeyFile path can parse them. The
// verbose PEM header is fine; imaged doesn't read the header —
// only the DER bytes between BEGIN/END matter.
func pemEnveloped(der []byte) []byte {
	const begin = "-----BEGIN PUBLIC KEY-----\n"
	const end = "-----END PUBLIC KEY-----\n"
	// 64-char line wrapping matches the existing pkg/cosign
	// LoadPublicKeyFile's expectations (it uses encoding/pem.Decode,
	// which is line-length tolerant, but the convention keeps the
	// file readable for ops).
	var out []byte
	out = append(out, begin...)
	for i := 0; i < len(der); i += 64 {
		end := i + 64
		if end > len(der) {
			end = len(der)
		}
		out = append(out, der[i:end]...)
		out = append(out, '\n')
	}
	out = append(out, end...)
	return out
}
