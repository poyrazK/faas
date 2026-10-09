package runtimequalification

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// NativeConfig contains operator-selected paths, never customer API input.
// The signing key is kept outside this object and the child environment.
type NativeConfig struct {
	SourceDirectory, GoBinary, GoSHA256, KernelPath, FirecrackerVersion, OutputDirectory string
}

type NativeIdentity struct{ HostID, KernelBootID string }
type CommandResult struct {
	ExitCode       int
	Stdout, Stderr []byte
}

// NativeSession is an internal acceptance-owner seam for synthetic tests.
// Production callers must use OpenNativeSession: no environment bypass exists.
type NativeSession interface {
	Identity() NativeIdentity
	Prepare(context.Context, Artifacts, Fixture) error
	Test(context.Context) (CommandResult, error)
	Leakcheck(context.Context) (CommandResult, error)
	Recheck(context.Context) error
	Close(context.Context) error
}
type NativeOpener func(context.Context, NativeConfig, Fixture) (NativeSession, error)

type Collection struct{ Envelope, TestMetal, Leakcheck []byte }

var nativeFCVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

func validateCollection(c NativeConfig, f Fixture, trust Trust, key ed25519.PrivateKey) error {
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if _, err := DecodeFixture(raw); err != nil {
		return err
	}
	if err := trust.validate(); err != nil {
		return err
	}
	if len(key) != ed25519.PrivateKeySize || !bytes.Equal(key.Public().(ed25519.PublicKey), trust.PublicKey) || trust.ReleaseID != f.Target.ID || trust.HostID != f.HostID || trust.SourceCommit != f.SourceCommit || trust.RunID != f.RunID {
		return fmt.Errorf("collector differs from operator trust pins: %w", ErrEvidence)
	}
	if !validHex(c.GoSHA256, 64) || !nativeFCVersion.MatchString(c.FirecrackerVersion) {
		return fmt.Errorf("pinned Go digest or Firecracker version absent: %w", ErrEvidence)
	}
	for _, path := range []string{c.SourceDirectory, c.GoBinary, c.KernelPath, c.OutputDirectory} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("native paths must be canonical absolute paths: %w", ErrEvidence)
		}
	}
	if pathWithin(c.SourceDirectory, c.OutputDirectory) {
		return fmt.Errorf("evidence output must be outside source: %w", ErrEvidence)
	}
	return nil
}

func pathWithin(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && (relative == "." || filepath.IsLocal(relative))
}

func expectedNative(f Fixture, id NativeIdentity) Observation {
	return Observation{RunID: f.RunID, ReleaseID: f.Target.ID, HostID: id.HostID, KernelBootID: id.KernelBootID, SourceCommit: f.SourceCommit, DeploymentID: f.DeploymentID, LayerKey: f.LayerKey,
		BaseSHA256: f.Target.BaseSHA256, GuestInitSHA256: f.Target.GuestInitSHA256, LayerSHA256: f.LayerSHA256, KernelSHA256: f.KernelSHA256, FirecrackerSHA256: f.FirecrackerSHA256,
		OS: "linux", Architecture: "amd64", Virtualization: "none", KVM: true, ColdBoot: true, Ready: true, Retired: true}
}

// Collect captures and signs one guarded attempt. It writes no receipt: callers
// import the persisted bundle only after this function returns successfully.
// Failed runs retain diagnostic logs but never a signed success envelope.
func Collect(ctx context.Context, store Store, artifacts Artifacts, c NativeConfig, f Fixture, trust Trust, key ed25519.PrivateKey, open NativeOpener) (out Collection, err error) {
	if store == nil || artifacts == nil || open == nil {
		return out, ErrEvidence
	}
	if err = validateCollection(c, f, trust, key); err != nil {
		return out, err
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if _, err = verifyPublishedTarget(ctx, store, expectedNative(f, NativeIdentity{HostID: f.HostID})); err != nil {
		return out, err
	}
	// Refuse an existing directory, including a prior attempt or symlink. Every
	// output file is created exclusively; neither successes nor failures overwrite.
	if err = os.Mkdir(c.OutputDirectory, 0o700); err != nil {
		return out, fmt.Errorf("create fresh evidence directory: %w", err)
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return out, err
	}
	if err = writeEvidence(c.OutputDirectory, "fixture.json", raw); err != nil {
		return out, err
	}
	session, err := open(ctx, c, f)
	if err != nil {
		return out, err
	}
	closed := false
	defer func() {
		if !closed {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), api.RuntimeQualificationCleanupTimeout)
			defer stop()
			err = errors.Join(err, session.Close(cleanup))
		}
	}()
	id := session.Identity()
	if id.HostID != trust.HostID || !validUUID(id.KernelBootID) {
		return out, fmt.Errorf("native identity differs from selected host: %w", ErrEvidence)
	}
	if err = session.Prepare(ctx, artifacts, f); err != nil {
		return out, err
	}
	started := time.Now().UTC()
	testCtx, stopTest := context.WithTimeout(ctx, api.RuntimeQualificationTestTimeout)
	metal, testErr := session.Test(testCtx)
	stopTest()
	out.TestMetal = metal.Stdout
	// Always run final leakcheck after the test process has stopped, including
	// cancellation and failures. This cleanup context carries caller values.
	cleanup, stopCleanup := context.WithTimeout(context.WithoutCancel(ctx), api.RuntimeQualificationCleanupTimeout)
	defer stopCleanup()
	leak, leakErr := session.Leakcheck(cleanup)
	out.Leakcheck = leak.Stdout
	completed := time.Now().UTC()
	persistErr := errors.Join(writeEvidence(c.OutputDirectory, "test-metal.jsonl", metal.Stdout), writeEvidence(c.OutputDirectory, "test-metal.stderr", metal.Stderr), writeEvidence(c.OutputDirectory, "leakcheck.log", leak.Stdout), writeEvidence(c.OutputDirectory, "leakcheck.stderr", leak.Stderr))
	status, _ := json.Marshal(struct{ TestMetal, Leakcheck int }{metal.ExitCode, leak.ExitCode})
	persistErr = errors.Join(persistErr, writeEvidence(c.OutputDirectory, "process-exits.json", status))
	if testErr != nil || leakErr != nil || persistErr != nil {
		return out, errors.Join(testErr, leakErr, persistErr)
	}
	if metal.ExitCode != 0 || leak.ExitCode != 0 {
		return out, fmt.Errorf("native test or final leakcheck failed (%d/%d): %w", metal.ExitCode, leak.ExitCode, ErrEvidence)
	}
	report := Report{Version: Version, Profile: state.RuntimeQualificationProfile, StartedAt: started, CompletedAt: completed, Native: expectedNative(f, id), TestMetalSHA256: SHA256(metal.Stdout), LeakcheckSHA256: SHA256(leak.Stdout), TestMetalExitCode: &metal.ExitCode, LeakcheckExitCode: &leak.ExitCode}
	if err = VerifyLogs(report, metal.Stdout, leak.Stdout); err != nil {
		return out, err
	}
	if err = session.Recheck(cleanup); err != nil {
		return out, err
	}
	err = session.Close(cleanup)
	closed = true
	if err != nil {
		return out, err
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	out.Envelope, err = EncodeEnvelope(report, metal.Stdout, leak.Stdout, key)
	if err != nil {
		return out, err
	}
	if err = writeEvidence(c.OutputDirectory, "report.json", out.Envelope); err != nil {
		out.Envelope = nil
		return out, err
	}
	if err = syncEvidenceDirectory(c.OutputDirectory); err != nil {
		out.Envelope = nil
		return out, err
	}
	return out, nil
}

func writeEvidence(directory, name string, raw []byte) error {
	//nolint:gosec // Private, newly created operator directory; fixed internal filenames and exclusive creation.
	file, err := os.OpenFile(filepath.Join(directory, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create native evidence file: %w", err)
	}
	_, writeErr := file.Write(raw)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return fmt.Errorf("retain native evidence file: %w", err)
	}
	return nil
}

func syncEvidenceDirectory(directory string) error {
	//nolint:forbidigo,gosec // Fresh private operator directory; fsync before allowing import of retained bundle.
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}
