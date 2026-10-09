// Command runtime-qualification-collect is a private native acceptance owner.
// It captures a guarded attempt and imports only successfully retained evidence.
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
	"path/filepath"
	"syscall"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/runtimequalification"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

type options struct {
	fixture, keyFile, publicKey, release, host, commit, run string
	native                                                  runtimequalification.NativeConfig
}
type resources struct {
	store     runtimequalification.Store
	artifacts runtimequalification.Artifacts
	close     func()
}
type dependencies struct {
	guard   func(context.Context) error
	loadKey func(string, ed25519.PublicKey) (ed25519.PrivateKey, error)
	open    func(context.Context) (resources, error)
	native  runtimequalification.NativeOpener
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	deps := dependencies{runtimequalification.CheckNativeCollectorHost, runtimequalification.ReadNativeSigningSeed, liveResources, runtimequalification.OpenNativeSession}
	if err := run(ctx, os.Args[1:], deps, os.Stdout); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintf(os.Stderr, "runtime-qualification-collect: %v\n", err)
		os.Exit(1)
	}
}

func parseOptions(args []string, out io.Writer) (options, error) {
	var o options
	f := flag.NewFlagSet("runtime-qualification-collect", flag.ContinueOnError)
	f.SetOutput(out)
	f.StringVar(&o.fixture, "fixture", "", "exact published native fixture JSON path")
	f.StringVar(&o.keyFile, "signing-seed-file", "", "root-owned 0600 file containing one 32-byte Ed25519 seed, outside source/output")
	f.StringVar(&o.publicKey, "public-key", "", "independently pinned Ed25519 public key in lowercase hex")
	f.StringVar(&o.release, "release", "", "expected runtime release ID")
	f.StringVar(&o.host, "host", "", "expected designated native host UUID")
	f.StringVar(&o.commit, "source-commit", "", "expected acceptance source commit")
	f.StringVar(&o.run, "run", "", "fresh selected native acceptance run UUID")
	f.StringVar(&o.native.SourceDirectory, "source-dir", "", "protected Git checkout containing the selected commit")
	f.StringVar(&o.native.GoBinary, "go", "", "root-protected pinned Go executable")
	f.StringVar(&o.native.GoSHA256, "go-sha256", "", "independently pinned Go executable SHA-256")
	f.StringVar(&o.native.KernelPath, "kernel", "", "root-protected kernel whose hash is in fixture")
	f.StringVar(&o.native.FirecrackerVersion, "firecracker-version", "", "installed pinned Firecracker version, e.g. 1.7.0")
	f.StringVar(&o.native.OutputDirectory, "output-dir", "", "new evidence directory under a protected parent, outside source")
	if err := f.Parse(args); err != nil {
		return o, err
	}
	if f.NArg() != 0 {
		return o, fmt.Errorf("positional arguments are unsupported")
	}
	for _, value := range []string{o.fixture, o.keyFile, o.publicKey, o.release, o.host, o.commit, o.run, o.native.SourceDirectory, o.native.GoBinary, o.native.GoSHA256, o.native.KernelPath, o.native.FirecrackerVersion, o.native.OutputDirectory} {
		if value == "" {
			return o, fmt.Errorf("all paths, versions and independent trust pins are required")
		}
	}
	if !filepath.IsAbs(o.keyFile) || pathWithin(o.native.SourceDirectory, o.keyFile) || pathWithin(o.native.OutputDirectory, o.keyFile) {
		return o, fmt.Errorf("signing seed must be outside source and output")
	}
	return o, nil
}

func validateSigningLocation(o options) error {
	source, err := filepath.EvalSymlinks(o.native.SourceDirectory)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(o.native.OutputDirectory))
	if err != nil {
		return err
	}
	key, err := filepath.EvalSymlinks(o.keyFile)
	if err != nil {
		return err
	}
	output := filepath.Join(parent, filepath.Base(o.native.OutputDirectory))
	if pathWithin(source, key) || pathWithin(output, key) || pathWithin(source, output) {
		return fmt.Errorf("resolved signing seed and evidence must be outside source, and seed outside output")
	}
	return nil
}

func pathWithin(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && (relative == "." || filepath.IsLocal(relative))
}

func run(ctx context.Context, args []string, deps dependencies, out io.Writer) error {
	o, err := parseOptions(args, out)
	if err != nil {
		return err
	}
	pub, err := hex.DecodeString(o.publicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize || hex.EncodeToString(pub) != o.publicKey {
		return fmt.Errorf("public key must be 32 bytes of lowercase hex")
	}
	if err := deps.guard(ctx); err != nil {
		return err
	}
	//nolint:forbidigo,gosec // Private operator fixture, captured with an explicit byte bound before infrastructure opens.
	file, err := os.Open(o.fixture)
	if err != nil {
		return err
	}
	raw, readErr := runtimequalification.ReadBounded(file, api.RuntimeQualificationReportMaxBytes)
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	fixture, err := runtimequalification.DecodeFixture(raw)
	if err != nil {
		return err
	}
	if fixture.Target.ID != o.release || fixture.HostID != o.host || fixture.SourceCommit != o.commit || fixture.RunID != o.run {
		return fmt.Errorf("fixture differs from independent trust pins")
	}
	if err := validateSigningLocation(o); err != nil {
		return err
	}
	key, err := deps.loadKey(o.keyFile, ed25519.PublicKey(pub))
	if err != nil {
		return err
	}
	defer clear(key)
	r, err := deps.open(ctx)
	if err != nil {
		return err
	}
	if r.close != nil {
		defer r.close()
	}
	trust := runtimequalification.Trust{PublicKey: pub, ReleaseID: o.release, HostID: o.host, SourceCommit: o.commit, RunID: o.run}
	bundle, err := runtimequalification.Collect(ctx, r.store, r.artifacts, o.native, fixture, trust, key, deps.native)
	if err != nil {
		return err
	}
	receipt, err := runtimequalification.Import(ctx, r.store, r.artifacts, runtimequalification.Inputs{Envelope: bytes.NewReader(bundle.Envelope), TestMetal: bytes.NewReader(bundle.TestMetal), Leakcheck: bytes.NewReader(bundle.Leakcheck)}, trust)
	if err != nil {
		return fmt.Errorf("import completed native bundle (retained at %s): %w", o.native.OutputDirectory, err)
	}
	_, err = fmt.Fprintf(out, "qualified runtime %s; report=%s; evidence=%s\n", receipt.ReleaseID, receipt.ReportSHA256, o.native.OutputDirectory)
	return err
}

func liveResources(ctx context.Context) (resources, error) {
	pool, err := db.OpenWithAppName(ctx, "", "faas-runtime-qualification-collect")
	if err != nil {
		return resources{}, err
	}
	artifacts, err := storage.BackendFromEnvContext(ctx)
	if err != nil {
		pool.Close()
		return resources{}, err
	}
	return resources{state.NewPgStore(pool), artifacts, pool.Close}, nil
}
