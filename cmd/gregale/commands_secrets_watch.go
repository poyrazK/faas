package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type secretWatchEvent struct {
	SchemaVersion int                        `json:"schema_version"`
	Type          string                     `json:"type"`
	ObservedAt    time.Time                  `json:"observed_at"`
	App           string                     `json:"app_slug"`
	Scope         string                     `json:"scope"`
	Summary       *api.AppSecretListResponse `json:"summary,omitempty"`
	Message       string                     `json:"message,omitempty"`
}

// Compare metadata independently of row/observation order and clock-only churn.
// Hashes and host sealing identities do not describe delivery progress.
func secretWatchFingerprint(resp api.AppSecretListResponse) ([]byte, error) {
	data, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var normalize func(any) any
	normalize = func(v any) any {
		switch item := v.(type) {
		case map[string]any:
			for key, child := range item {
				switch key {
				case "created_at", "updated_at", "last_delivery_attempt_at", "last_delivered_at", "last_runtime_reload_at", "observed_at", "application_ack_at", "value_hash", "kid":
					delete(item, key)
				default:
					item[key] = normalize(child)
				}
			}
		case []any:
			for i, child := range item {
				item[i] = normalize(child)
			}
			sort.Slice(item, func(i, j int) bool {
				a, _ := json.Marshal(item[i])
				b, _ := json.Marshal(item[j])
				return bytes.Compare(a, b) < 0
			})
		}
		return v
	}
	return json.Marshal(normalize(value))
}

func watchSecretList(client *Client, app, scope, class, olderThan, delivery string, age, interval, timeout time.Duration, keys []string) int {
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx := signalCtx
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	var lastSnapshot *api.AppSecretListResponse
	var hints []secretKeyHint
	emit := func(kind string, resp *api.AppSecretListResponse, message string) error {
		event := secretWatchEvent{SchemaVersion: 1, Type: kind, ObservedAt: time.Now().UTC(), App: app, Scope: scopeOrDefault(scope), Summary: resp, Message: message}
		if jsonOutput {
			return json.NewEncoder(osStdout).Encode(event)
		}
		if _, err := fmt.Fprintf(osStdout, "\n[%s] %s\n", event.ObservedAt.Format(time.RFC3339), kind); err != nil {
			return err
		}
		if resp == nil {
			_, err := fmt.Fprintln(osStdout, message)
			return err
		}
		if kind == "change" && lastSnapshot != nil {
			return emitSecretWatchChangesHuman(app, scope, class, olderThan, delivery, lastSnapshot, resp, hints, keys)
		}
		if err := emitSecretWatchHuman(app, scope, class, olderThan, delivery, resp, hints, keys...); err != nil {
			return err
		}
		return nil
	}
	if !jsonOutput {
		_, _ = fmt.Fprintf(osStdout, "Watching secret metadata for %s (scope=%s) every %s. Ctrl-C to exit.\n", compactInspectFailureText(app), compactInspectFailureText(scopeOrDefault(scope)), interval)
	}
	var previous []byte
	unavailable := false
	var watchErr error
	for ctx.Err() == nil {
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		resp, err := client.ListSecretsWithScope(readCtx, app, scope)
		cancel()
		if ctx.Err() != nil {
			break
		}
		if err != nil {
			if inspectWatchPermanentError(err) {
				watchErr = err
				break
			}
			if !unavailable {
				watchErr = emit("unavailable", nil, "Secret metadata could not be read; previous observations may be stale. Retrying.")
				if watchErr != nil {
					break
				}
			}
			unavailable, previous, lastSnapshot = true, nil, nil
		} else {
			availableKeys := secretMetadataKeys(&resp)
			filterAppSecretListByClass(&resp, class)
			if age > 0 {
				filterAppSecretListByAge(&resp, time.Now().Add(-age))
			}
			filterAppSecretListByDelivery(&resp, delivery)
			filterAppSecretListByKeys(&resp, keys)
			hints = secretKeyHints(keys, availableKeys, secretMetadataKeys(&resp))
			fingerprint, err := secretWatchFingerprint(resp)
			if err != nil {
				watchErr = err
				break
			}
			if !jsonOutput {
				guidance, _ := json.Marshal(hints)
				fingerprint = append(fingerprint, guidance...)
			}
			if previous == nil || !bytes.Equal(previous, fingerprint) {
				kind := "change"
				if previous == nil {
					kind = "snapshot"
				}
				watchErr = emit(kind, &resp, "")
				if watchErr != nil {
					break
				}
			}
			if !jsonOutput {
				copy := resp
				lastSnapshot = &copy
			}
			previous, unavailable = fingerprint, false
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	if watchErr == nil {
		watchErr = ctx.Err()
	}
	if errors.Is(watchErr, context.Canceled) {
		return 130
	}
	if errors.Is(watchErr, context.DeadlineExceeded) {
		_, _ = fmt.Fprintln(osStderr, "Secret watch duration elapsed; previous observations may be stale.")
		return 124
	}
	if watchErr != nil {
		return printErr("Secret watch stopped", watchErr)
	}
	return 0
}

// Existing human renderers discard individual write errors. Track their output
// so a closed pipe stops the polling loop instead of continuing API reads.
type secretWatchWriter struct {
	target io.Writer
	err    error
}

func (w *secretWatchWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	n, err := w.target.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	w.err = err
	return n, err
}
func emitSecretWatchHuman(app, scope, class, olderThan, delivery string, resp *api.AppSecretListResponse, hints []secretKeyHint, keys ...string) error {
	original := osStdout
	writer := &secretWatchWriter{target: original}
	osStdout = writer
	defer func() { osStdout = original }()
	renderSecretListHuman(app, scope, class, olderThan, delivery, resp, keys...)
	printSecretKeyHints(app, scope, hints)
	return writer.err
}
