package imaged

// ADR-430: signature attachment errors preserve missing/invalid/unavailable
// distinctions and the exact scoped transport evidence.

// handler_coverage_test.go: covers 11 zero-coverage helpers on
// pkg/imaged/handler.go that the existing test files do not reach.
// These are all pure-logic setters / cache helpers / puller adapters;
// no Firecracker, no real network. Each test exercises one helper's
// happy path + nil/empty guards so future refactors can't silently
// drop the load-bearing branches.

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

// nopVMMClient implements VMMClientIface for CloseVMMClient tests
// without pulling in a real vmmd gRPC client.
type nopVMMClient struct {
	closed        bool
	mountErr      error
	umountErr     error
	overlayErr    error
	uoverlayErr   error
	mountReturns  string
	mountCalled   int
	umountCalled  int
	overlayCalled int
	uoverlayCount int
}

func (n *nopVMMClient) MountParentExt4ReadOnly(_ context.Context, _ string) (string, error) {
	n.mountCalled++
	return n.mountReturns, n.mountErr
}
func (n *nopVMMClient) MaterializeParentExt4(_ context.Context, _, _ string) error {
	n.mountCalled++
	return n.mountErr
}
func (n *nopVMMClient) UmountParentExt4(_ context.Context, _ string) error {
	n.umountCalled++
	return n.umountErr
}
func (n *nopVMMClient) MountOverlayParent(_ context.Context, _, _, _, _ string) error {
	n.overlayCalled++
	return n.overlayErr
}
func (n *nopVMMClient) UmountOverlayParent(_ context.Context, _ string) error {
	n.uoverlayCount++
	return n.uoverlayErr
}
func (n *nopVMMClient) Close() error { n.closed = true; return nil }

// newTestHandler builds a minimal Handler with no real collaborators.
// Used by every test below; the trust / puller / VMMClient paths are
// unit-injected.
func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	return New(state.NewMemStore(), nil, nil, nil, "", t.TempDir(),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// TestWithTrustedPublishersDir_EmptySkipsLoad — WithTrustedPublishersDir("")
// must NOT call refreshTrustedPublishers (which would log a warn if the
// empty string were passed as a path). The setter is silent on empty
// input — the verify path checks trustedPublishersDir and gates on
// empty.
func TestWithTrustedPublishersDir_EmptySkipsLoad(t *testing.T) {
	h := newTestHandler(t)
	ret := h.WithTrustedPublishersDir("")
	if ret != h {
		t.Errorf("WithTrustedPublishersDir returned %p, want same receiver %p", ret, h)
	}
	if h.trustedPublishersDir != "" {
		t.Errorf("trustedPublishersDir = %q, want empty", h.trustedPublishersDir)
	}
}

// TestWithTrustedPublishersDir_NonexistentDirLogsAndContinues — when
// the dir is set but doesn't exist, refreshTrustedPublishers errors,
// the setter logs a warning, and the handler returns itself (does
// NOT propagate the error — the cmd/imaged wiring continues with an
// empty cache per the doc-comment on WithTrustedPublishersDir).
func TestWithTrustedPublishersDir_NonexistentDirLogsAndContinues(t *testing.T) {
	h := newTestHandler(t)
	dir := filepath.Join(t.TempDir(), "missing")
	ret := h.WithTrustedPublishersDir(dir)
	if ret != h {
		t.Errorf("WithTrustedPublishersDir returned %p, want same receiver %p", ret, h)
	}
	if h.trustedPublishersDir != dir {
		t.Errorf("trustedPublishersDir = %q, want %q", h.trustedPublishersDir, dir)
	}
}

// TestRefreshTrustedPublishers_EmptyDirClearsCache — refreshTrustedPublishers()
// with trustedPublishersDir == "" clears the cache + flag and returns nil.
// Pins the "disable signature verification" branch.
func TestRefreshTrustedPublishers_EmptyDirClearsCache(t *testing.T) {
	h := newTestHandler(t)
	h.trustedPublishersCache = map[string][]cosign.TrustedPublisher{
		"app-1": {{Name: "stub"}},
	}
	h.trustedPublishersCacheOK = true
	if err := h.refreshTrustedPublishers(); err != nil {
		t.Fatalf("refreshTrustedPublishers: %v", err)
	}
	if h.trustedPublishersCache != nil {
		t.Errorf("trustedPublishersCache = %v, want nil after clear", h.trustedPublishersCache)
	}
	if h.trustedPublishersCacheOK {
		t.Errorf("trustedPublishersCacheOK = true, want false after clear")
	}
}

// TestSnapshotTrustedPublishers_NilWhenCacheEmpty — exercise the
// "cache disabled" branch: returns nil when the cache flag is off.
func TestSnapshotTrustedPublishers_NilWhenCacheEmpty(t *testing.T) {
	h := newTestHandler(t)
	got := h.snapshotTrustedPublishers("app-1")
	if got != nil {
		t.Errorf("snapshotTrustedPublishers(empty cache) = %v, want nil", got)
	}
}

// TestSnapshotTrustedPublishers_ReturnsCachedOnHit — when the cache
// has a matching appID, snapshotTrustedPublishers returns its slice.
func TestSnapshotTrustedPublishers_ReturnsCachedOnHit(t *testing.T) {
	h := newTestHandler(t)
	want := []cosign.TrustedPublisher{{Name: "github-actions"}}
	h.trustedPublishersCache = map[string][]cosign.TrustedPublisher{"app-1": want}
	h.trustedPublishersCacheOK = true
	got := h.snapshotTrustedPublishers("app-1")
	if len(got) != 1 || got[0].Name != "github-actions" {
		t.Errorf("snapshotTrustedPublishers(app-1) = %v, want one entry 'github-actions'", got)
	}
}

// TestSnapshotTrustedPublishers_NilOnMiss — when the cache is OK but
// the appID is unknown, return nil (not a panic / not an empty slice
// masquerading as "configured"). Matches the verify path's len(pubs)==0
// check.
func TestSnapshotTrustedPublishers_NilOnMiss(t *testing.T) {
	h := newTestHandler(t)
	h.trustedPublishersCache = map[string][]cosign.TrustedPublisher{
		"app-other": {{Name: "x"}},
	}
	h.trustedPublishersCacheOK = true
	if got := h.snapshotTrustedPublishers("app-1"); got != nil {
		t.Errorf("snapshotTrustedPublishers(app-1) on miss = %v, want nil", got)
	}
}

// TestEmitSignatureAudit_NoNotifierSafe — emitSignatureAudit must not
// panic when the notifier is nil (the canonical unit-test shape;
// handler_test.go's newTestHandler uses a nil notifier for every
// other test).
func TestEmitSignatureAudit_NoNotifierSafe(t *testing.T) {
	h := newTestHandler(t)
	app := state.App{ID: "app-1", Slug: "demo"}
	dep := state.Deployment{ID: "dep-1"}
	// Must not panic with nil notifier.
	h.emitSignatureAudit(context.Background(), "app.signature_missing", app, dep, "ghcr.io/me/app@sha256:deadbeef", "")
}

// TestEmitSignatureAudit_WithNotifierWritesChannel — when the
// notifier is set, emitSignatureAudit fires a notify on the
// "audit_event" channel with the documented JSON shape (kind +
// app_id + deployment_id + ref + signer).
func TestEmitSignatureAudit_WithNotifierWritesChannel(t *testing.T) {
	h := newTestHandler(t)
	notif := &fakeNotifier{}
	h.notif = notif

	app := state.App{ID: "app-1", Slug: "demo"}
	dep := state.Deployment{ID: "dep-1"}
	h.emitSignatureAudit(context.Background(), "app.signature_missing", app, dep, "ghcr.io/me/app@sha256:deadbeef", "")

	c := findNotify(notif, "audit_event")
	if c == nil {
		t.Fatal("emitSignatureAudit: no audit_event notify fired (notifier wired?)")
	}
	for _, want := range []string{"app.signature_missing", "app-1", "dep-1", "ghcr.io/me/app@sha256:deadbeef"} {
		if !strings.Contains(c.payload, want) {
			t.Errorf("audit payload missing %q (payload=%s)", want, c.payload)
		}
	}
}

// stubPuller satisfies oci.Puller so we can drive ResolveDigest /
// signature attachments without bringing in the network. PullImageConfig /
// PullLayers return errors — they're not exercised by these paths
// (image build readers are separate from attachment readers).
type stubPuller struct {
	digest      string
	digestErr   error
	config      oci.ImageConfig
	configErr   error
	layers      oci.PullLayersResult
	layersErr   error
	manifest    oci.Manifest
	manifestErr error
	blob        io.ReadCloser
	blobErr     error

	manifestPuller bool
}

func (s *stubPuller) PullDigest(_ context.Context, _ string) (string, error) {
	return s.digest, s.digestErr
}
func (s *stubPuller) PullImageConfig(_ context.Context, _ string) (oci.ImageConfig, error) {
	return s.config, s.configErr
}
func (s *stubPuller) PullLayers(_ context.Context, _ string) (oci.PullLayersResult, error) {
	return s.layers, s.layersErr
}
func (s *stubPuller) PullManifest(_ context.Context, _ string) (oci.Manifest, error) {
	return s.manifest, s.manifestErr
}
func (s *stubPuller) PullBlob(_ context.Context, _, _ string) (io.ReadCloser, error) {
	return s.blob, s.blobErr
}

// Unsupported attachment transports fail closed without pretending a registry
// returned a 404. Network/authentication failures remain distinct from missing.
func TestSignatureAttachmentAdapterErrors(t *testing.T) {
	ctx := context.Background()
	p := &ociImageSignaturePuller{oci: onlyPullerStub{}}
	if _, err := p.FetchSignatureAttachments(ctx, "ghcr.io/me/app", "sha256:abc"); err == nil || errors.Is(err, cosign.ErrSignatureMissing) {
		t.Fatalf("unsupported attachment transport: %v", err)
	}
	for _, tc := range []struct {
		name         string
		source, want error
	}{
		{"missing", oci.ErrImageSignatureMissing, cosign.ErrSignatureMissing},
		{"malformed", oci.ErrImageManifestInvalid, cosign.ErrSignatureInvalid},
		{"network", io.ErrUnexpectedEOF, io.ErrUnexpectedEOF},
		{"cancelled", context.Canceled, context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &ociImageSignaturePuller{oci: &attachmentPullerStub{err: tc.source}}
			_, err := p.FetchSignatureAttachments(ctx, "ghcr.io/me/app", "sha256:abc")
			if !errors.Is(err, tc.want) || !errors.Is(err, tc.source) {
				t.Fatalf("classification lost: %v", err)
			}
			if tc.name != "missing" && errors.Is(err, cosign.ErrSignatureMissing) {
				t.Fatal("non-404 misclassified as missing")
			}
		})
	}
}

type onlyPullerStub struct {
	digest    string
	digestErr error
}

func (o onlyPullerStub) PullDigest(context.Context, string) (string, error) {
	return o.digest, o.digestErr
}
func (o onlyPullerStub) PullImageConfig(context.Context, string) (oci.ImageConfig, error) {
	return oci.ImageConfig{}, nil
}
func (o onlyPullerStub) PullLayers(context.Context, string) (oci.PullLayersResult, error) {
	return oci.PullLayersResult{}, nil
}

type attachmentPullerStub struct {
	onlyPullerStub
	err         error
	attachments []oci.ImageSignatureAttachment
	ctx         context.Context
	ref, digest string
	auth        *oci.BasicAuth
}

func (p *attachmentPullerStub) PullImageSignatureAttachments(ctx context.Context, ref, digest string, auth *oci.BasicAuth) ([]oci.ImageSignatureAttachment, error) {
	p.ctx, p.ref, p.digest, p.auth = ctx, ref, digest, auth
	return p.attachments, p.err
}

func TestSignatureAttachmentAdapterPreservesScopeAndEvidence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	auth := &oci.BasicAuth{Username: "user", Password: "secret"}
	a := oci.ImageSignatureAttachment{ManifestDigest: "manifest", PayloadDigest: "payload", Payload: []byte("claim"), Signature: []byte("sig")}
	stub := &attachmentPullerStub{attachments: []oci.ImageSignatureAttachment{a}}
	p := &ociImageSignaturePuller{oci: stub, auth: auth}
	got, err := p.FetchSignatureAttachments(ctx, "ghcr.io/me/app", "subject")
	if err != nil || len(got) != 1 || got[0].ManifestDigest != a.ManifestDigest || got[0].PayloadDigest != a.PayloadDigest || string(got[0].Payload) != string(a.Payload) || string(got[0].Signature) != string(a.Signature) {
		t.Fatalf("evidence: %+v %v", got, err)
	}
	if stub.ctx != ctx || stub.ref != "ghcr.io/me/app" || stub.digest != "subject" || stub.auth != auth {
		t.Fatal("attachment read lost authority context")
	}
}

func TestSignatureAttachmentResolveError(t *testing.T) {
	p := &ociImageSignaturePuller{oci: onlyPullerStub{digestErr: context.Canceled}}
	if _, err := p.ResolveDigest(context.Background(), "ghcr.io/me/app:latest"); !errors.Is(err, context.Canceled) {
		t.Fatalf("resolve: %v", err)
	}
}

// TestWithDeployBaseRef_SetsField — the setter is a one-liner; pin
// it so future refactors don't accidentally swallow the assignment.
func TestWithDeployBaseRef_SetsField(t *testing.T) {
	h := newTestHandler(t)
	const ref = "mirror.gcr.io/library/alpine:3.19"
	if got := h.WithDeployBaseRef(ref); got != h {
		t.Errorf("WithDeployBaseRef returned %p, want same receiver %p", got, h)
	}
	if h.deployBaseRefOverride != ref {
		t.Errorf("deployBaseRefOverride = %q, want %q", h.deployBaseRefOverride, ref)
	}
}

// TestWithSyftRun_SetsField — pin the WithSyftRun setter.
func TestWithSyftRun_SetsField(t *testing.T) {
	h := newTestHandler(t)
	called := false
	fn := func(_ context.Context, _ string) ([]byte, error) {
		called = true
		return []byte("sbom"), nil
	}
	if got := h.WithSyftRun(fn); got != h {
		t.Errorf("WithSyftRun returned %p, want same receiver %p", got, h)
	}
	if h.syftRun == nil {
		t.Fatal("syftRun not wired")
	}
	if _, err := h.syftRun(context.Background(), ""); err != nil {
		t.Errorf("syftRun: %v", err)
	}
	if !called {
		t.Errorf("syftRun closure was not invoked")
	}
}

// TestWithVMMClient_SetsField — pin the WithVMMClient setter; the
// returned handler must carry the client so CloseVMMClient can
// dispatch to it.
func TestWithVMMClient_SetsField(t *testing.T) {
	h := newTestHandler(t)
	c := &nopVMMClient{}
	if got := h.WithVMMClient(c); got != h {
		t.Errorf("WithVMMClient returned %p, want same receiver %p", got, h)
	}
	if h.vmmClient != c {
		t.Errorf("vmmClient = %p, want %p", h.vmmClient, c)
	}
}

// TestCloseVMMClient_NilReceiverSafe — nil-receiver + nil-client
// branches must both be no-ops (idempotent on the cmd/imaged
// SIGTERM path).
func TestCloseVMMClient_NilReceiverSafe(t *testing.T) {
	var nilH *Handler
	if err := nilH.CloseVMMClient(); err != nil {
		t.Errorf("nilH.CloseVMMClient = %v, want nil", err)
	}
	h := newTestHandler(t)
	if err := h.CloseVMMClient(); err != nil {
		t.Errorf("h.CloseVMMClient (no client) = %v, want nil", err)
	}
}

// TestCloseVMMClient_DispatchesToClient — when a client is wired,
// CloseVMMClient must call its Close method and propagate any error.
func TestCloseVMMClient_DispatchesToClient(t *testing.T) {
	h := newTestHandler(t)
	c := &nopVMMClient{}
	h.vmmClient = c
	if err := h.CloseVMMClient(); err != nil {
		t.Errorf("h.CloseVMMClient = %v, want nil", err)
	}
	if !c.closed {
		t.Errorf("vmmClient.Close() was not called")
	}
}

// TestWithTrustedPublishersDir_RealDirWithNoFiles — when the dir
// exists but is empty, refreshTrustedPublishers succeeds (no entries)
// and the cache is populated empty + cacheOK=true. The cmd/imaged
// startup path expects "no PEM files" to be a valid empty-trust-list
// state, not an error.
func TestWithTrustedPublishersDir_RealDirWithNoFiles(t *testing.T) {
	h := newTestHandler(t)
	dir := t.TempDir() // exists but empty
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("ignore"), 0o600); err != nil {
		t.Fatalf("write README: %v", err)
	}
	ret := h.WithTrustedPublishersDir(dir)
	if ret != h {
		t.Errorf("WithTrustedPublishersDir returned %p, want same receiver %p", ret, h)
	}
	if !h.trustedPublishersCacheOK {
		t.Errorf("trustedPublishersCacheOK = false after empty-dir load; want true")
	}
}

// TestNopVMMClient_SatisfiesInterface is a compile-time assertion that
// *nopVMMClient satisfies VMMClientIface. If a future PR adds a new
// method to the interface this test will fail to compile and force
// the test fake to be updated alongside the production code.
func TestNopVMMClient_SatisfiesInterface(t *testing.T) {
	var _ VMMClientIface = (*nopVMMClient)(nil)
	// Also exercise every method so the assertion isn't vacuous:
	c := &nopVMMClient{mountReturns: "/mnt/x"}
	if _, err := c.MountParentExt4ReadOnly(context.Background(), "key"); err != nil {
		t.Errorf("MountParentExt4ReadOnly: %v", err)
	}
	if err := c.UmountParentExt4(context.Background(), "/mnt/x"); err != nil {
		t.Errorf("UmountParentExt4: %v", err)
	}
	if err := c.MountOverlayParent(context.Background(), "l", "u", "w", "m"); err != nil {
		t.Errorf("MountOverlayParent: %v", err)
	}
	if err := c.UmountOverlayParent(context.Background(), "m"); err != nil {
		t.Errorf("UmountOverlayParent: %v", err)
	}
	if c.mountCalled != 1 || c.umountCalled != 1 || c.overlayCalled != 1 || c.uoverlayCount != 1 {
		t.Errorf("call counters wrong: m=%d u=%d o=%d uo=%d",
			c.mountCalled, c.umountCalled, c.overlayCalled, c.uoverlayCount)
	}
}

// _ pins the oci.Puller / ManifestPuller symbols so unused-import
// doesn't fire if a future refactor drops the stubPuller adapter.
var _ oci.Puller = (*stubPuller)(nil)
var _ oci.ManifestPuller = (*stubPuller)(nil)
