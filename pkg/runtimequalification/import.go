package runtimequalification

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

type Store interface {
	RuntimeReleaseByID(context.Context, string) (state.RuntimeRelease, error)
	RuntimeReleaseForArtifact(context.Context, string, string) (state.RuntimeRelease, error)
	DeploymentByID(context.Context, string) (state.Deployment, error)
	AppByID(context.Context, string) (state.App, error)
	RecordRuntimeReleaseQualification(context.Context, state.RuntimeReleaseQualification) (state.RuntimeReleaseQualification, error)
}

type Artifacts interface {
	Get(context.Context, string) (io.ReadCloser, error)
	Put(context.Context, string, io.Reader) error
}

type Inputs struct{ Envelope, TestMetal, Leakcheck io.Reader }

// Import makes the first production receipt write only after independent trust,
// log, catalogue/binding and published-byte checks. Evidence is archived and
// read back before the receipt; failures cannot grant upgrade eligibility.
func Import(ctx context.Context, store Store, artifacts Artifacts, inputs Inputs, trust Trust) (state.RuntimeReleaseQualification, error) {
	if store == nil || artifacts == nil || inputs.Envelope == nil || inputs.TestMetal == nil || inputs.Leakcheck == nil {
		return state.RuntimeReleaseQualification{}, ErrEvidence
	}
	if err := ctx.Err(); err != nil {
		return state.RuntimeReleaseQualification{}, err
	}
	raw, err := ReadBounded(contextReader{ctx, inputs.Envelope}, api.RuntimeQualificationReportMaxBytes)
	if err != nil {
		return state.RuntimeReleaseQualification{}, err
	}
	report, envelope, err := VerifyEnvelope(raw, trust, time.Now().UTC())
	if err != nil {
		return state.RuntimeReleaseQualification{}, err
	}
	metal, err := ReadBounded(contextReader{ctx, inputs.TestMetal}, api.RuntimeQualificationLogMaxBytes)
	if err != nil {
		return state.RuntimeReleaseQualification{}, err
	}
	leak, err := ReadBounded(contextReader{ctx, inputs.Leakcheck}, api.RuntimeQualificationLogMaxBytes)
	if err != nil {
		return state.RuntimeReleaseQualification{}, err
	}
	if err := VerifyLogs(report, metal, leak); err != nil {
		return state.RuntimeReleaseQualification{}, err
	}
	r, err := verifyPublishedInputs(ctx, store, artifacts, report.Native)
	if err != nil {
		return state.RuntimeReleaseQualification{}, err
	}
	digest := SHA256(envelope)
	for _, entry := range []struct {
		name string
		body []byte
	}{{"report.json", envelope}, {"test-metal.jsonl", metal}, {"leakcheck.log", leak}} {
		if err := retainEvidence(ctx, artifacts, EvidenceKey(r.ID, digest, entry.name), entry.body); err != nil {
			return state.RuntimeReleaseQualification{}, err
		}
	}
	n := report.Native
	q := state.RuntimeReleaseQualification{ReleaseID: r.ID, Profile: report.Profile, Architecture: r.Architecture, HostID: n.HostID, KernelBootID: n.KernelBootID, SourceCommit: n.SourceCommit,
		KernelSHA256: n.KernelSHA256, FirecrackerSHA256: n.FirecrackerSHA256, ReportSHA256: digest, TestMetalSHA256: report.TestMetalSHA256, LeakcheckSHA256: report.LeakcheckSHA256,
		StartedAt: report.StartedAt, CompletedAt: report.CompletedAt}
	if err := ctx.Err(); err != nil {
		return state.RuntimeReleaseQualification{}, err
	}
	q, err = store.RecordRuntimeReleaseQualification(ctx, q)
	if err != nil {
		return state.RuntimeReleaseQualification{}, fmt.Errorf("record verified native qualification: %w", err)
	}
	return q, nil
}

func verifyPublishedInputs(ctx context.Context, store Store, artifacts Artifacts, n Observation) (state.RuntimeRelease, error) {
	r, err := verifyPublishedTarget(ctx, store, n)
	if err != nil {
		return r, err
	}

	for _, entry := range []struct{ key, want string }{{r.BaseKey(), r.BaseSHA256}, {n.LayerKey, n.LayerSHA256}} {
		got, size, err := artifactSHA(ctx, artifacts, entry.key, 0)
		if err != nil {
			return r, err
		}
		if got != entry.want || size == 0 {
			return r, fmt.Errorf("native artifact bytes differ: %w", ErrEvidence)
		}
	}
	return r, nil
}

func verifyPublishedTarget(ctx context.Context, store Store, n Observation) (state.RuntimeRelease, error) {
	r, err := store.RuntimeReleaseByID(ctx, n.ReleaseID)
	if err != nil {
		return r, fmt.Errorf("read qualified runtime target: %w", err)
	}
	if r.Validate() != nil || r.ID != n.ReleaseID || r.Architecture != n.Architecture || r.BaseSHA256 != n.BaseSHA256 || r.GuestInitSHA256 != n.GuestInitSHA256 {
		return r, fmt.Errorf("native target differs from catalogue: %w", ErrEvidence)
	}
	d, err := store.DeploymentByID(ctx, n.DeploymentID)
	if err != nil {
		return r, fmt.Errorf("read native fixture deployment: %w", err)
	}
	a, err := store.AppByID(ctx, d.AppID)
	if err != nil {
		return r, fmt.Errorf("read native fixture application: %w", err)
	}
	if d.ID != n.DeploymentID || a.ID != d.AppID || d.RootfsKey != n.LayerKey || n.LayerKey == r.BaseKey() || n.LayerSHA256 == n.BaseSHA256 || a.Type != state.AppTypeFunction || a.Runtime != r.Runtime || a.Manifest.BuildDockerfile != "" {
		return r, fmt.Errorf("native fixture is not the selected managed function artifact: %w", ErrEvidence)
	}
	bound, err := store.RuntimeReleaseForArtifact(ctx, a.AccountID, n.LayerKey)
	if err != nil {
		return r, fmt.Errorf("read native fixture runtime binding: %w", err)
	}
	if bound.ID != r.ID {
		return r, fmt.Errorf("native fixture uses another runtime: %w", ErrEvidence)
	}
	return r, nil
}

func EvidenceKey(releaseID, reportSHA, name string) string {
	return "qualification/runtime-releases/" + releaseID + "/" + reportSHA + "/" + name
}

func retainEvidence(ctx context.Context, artifacts Artifacts, key string, raw []byte) error {
	want := SHA256(raw)
	got, size, err := artifactSHA(ctx, artifacts, key, int64(len(raw)))
	if err == nil {
		if got != want || size != int64(len(raw)) {
			return fmt.Errorf("retained native evidence is corrupt: %w", ErrEvidence)
		}
		return nil
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	if err := artifacts.Put(ctx, key, bytes.NewReader(raw)); err != nil {
		return fmt.Errorf("archive native evidence: %w", err)
	}
	got, size, err = artifactSHA(ctx, artifacts, key, int64(len(raw)))
	if err != nil {
		return err
	}
	if got != want || size != int64(len(raw)) {
		return fmt.Errorf("archived native evidence bytes differ: %w", ErrEvidence)
	}
	return nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func artifactSHA(ctx context.Context, artifacts Artifacts, key string, maxBytes int64) (string, int64, error) {
	body, err := artifacts.Get(ctx, key)
	if err != nil {
		return "", 0, fmt.Errorf("read native artifact/evidence: %w", err)
	}
	var r io.Reader = contextReader{ctx, body}
	if maxBytes > 0 {
		r = io.LimitReader(r, maxBytes+1)
	}
	h := sha256.New()
	size, readErr := io.Copy(h, r)
	closeErr := body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return "", 0, fmt.Errorf("hash native artifact/evidence: %w", err)
	}
	if maxBytes > 0 && size > maxBytes {
		return "", size, fmt.Errorf("retained native evidence exceeds expected size: %w", ErrEvidence)
	}
	return hex.EncodeToString(h.Sum(nil)), size, nil
}
