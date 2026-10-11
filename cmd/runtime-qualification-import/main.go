// Command runtime-qualification-import records verified operator-native evidence
// against an existing catalogue. It neither migrates the DB nor runs a VM.
package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/runtimequalification"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

type resources struct {
	store     runtimequalification.Store
	artifacts runtimequalification.Artifacts
	close     func()
}
type openResources func(context.Context) (resources, error)

type options struct{ report, metal, leak, publicKey, release, host, commit, run string }

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, os.Args[1:], liveResources, os.Stdout); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintf(os.Stderr, "runtime-qualification-import: %v\n", err)
		os.Exit(1)
	}
}

func parseOptions(args []string, out io.Writer) (options, error) {
	var o options
	f := flag.NewFlagSet("runtime-qualification-import", flag.ContinueOnError)
	f.SetOutput(out)
	f.StringVar(&o.report, "report", "", "signed native envelope path")
	f.StringVar(&o.metal, "test-metal", "", "raw go test -json evidence path")
	f.StringVar(&o.leak, "leakcheck", "", "final leakcheck output path")
	f.StringVar(&o.publicKey, "public-key", "", "operator-pinned Ed25519 public key in lowercase hex; never taken from evidence")
	f.StringVar(&o.release, "release", "", "expected runtime release ID")
	f.StringVar(&o.host, "host", "", "expected designated native host UUID")
	f.StringVar(&o.commit, "source-commit", "", "expected acceptance source commit")
	f.StringVar(&o.run, "run", "", "expected native acceptance run UUID")
	if err := f.Parse(args); err != nil {
		return o, err
	}
	if f.NArg() != 0 || o.report == "" || o.metal == "" || o.leak == "" || o.publicKey == "" || o.release == "" || o.host == "" || o.commit == "" || o.run == "" {
		return o, fmt.Errorf("all evidence paths and operator trust pins are required")
	}
	return o, nil
}

func run(ctx context.Context, args []string, open openResources, out io.Writer) error {
	o, err := parseOptions(args, out)
	if err != nil {
		return err
	}
	publicKey, err := hex.DecodeString(o.publicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize || hex.EncodeToString(publicKey) != o.publicKey {
		return fmt.Errorf("public key must be 32 bytes of lowercase hex")
	}
	trust := runtimequalification.Trust{PublicKey: ed25519.PublicKey(publicKey), ReleaseID: o.release, HostID: o.host, SourceCommit: o.commit, RunID: o.run}
	loaded := make([][]byte, 0, 3)
	for _, file := range []struct {
		path  string
		limit int
	}{{o.report, api.RuntimeQualificationReportMaxBytes}, {o.metal, api.RuntimeQualificationLogMaxBytes}, {o.leak, api.RuntimeQualificationLogMaxBytes}} {
		raw, err := readEvidence(file.path, file.limit)
		if err != nil {
			return err
		}
		loaded = append(loaded, raw)
	}
	// Reject bad signatures and incomplete runs before opening DB/storage.
	report, _, err := runtimequalification.VerifyEnvelope(loaded[0], trust, time.Now().UTC())
	if err != nil {
		return err
	}
	if err := runtimequalification.VerifyLogs(report, loaded[1], loaded[2]); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r, err := open(ctx)
	if err != nil {
		return fmt.Errorf("open operator qualification resources: %w", err)
	}
	if r.close != nil {
		defer r.close()
	}
	q, err := runtimequalification.Import(ctx, r.store, r.artifacts, runtimequalification.Inputs{Envelope: bytes.NewReader(loaded[0]), TestMetal: bytes.NewReader(loaded[1]), Leakcheck: bytes.NewReader(loaded[2])}, trust)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "qualified runtime %s; report=%s; recorded_at=%s\n", q.ReleaseID, q.ReportSHA256, q.RecordedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func readEvidence(path string, limit int) ([]byte, error) {
	//nolint:forbidigo,gosec // Private operator-selected evidence path; bounded read, never a customer path or signing key.
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open native evidence: %w", err)
	}
	raw, readErr := runtimequalification.ReadBounded(f, limit)
	closeErr := f.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return raw, nil
}

func liveResources(ctx context.Context) (resources, error) {
	pool, err := db.OpenWithAppName(ctx, "", "faas-runtime-qualification-import")
	if err != nil {
		return resources{}, err
	}
	artifacts, err := storage.BackendFromEnvContext(ctx)
	if err != nil {
		pool.Close()
		return resources{}, err
	}
	return resources{store: state.NewPgStore(pool), artifacts: artifacts, close: pool.Close}, nil
}
