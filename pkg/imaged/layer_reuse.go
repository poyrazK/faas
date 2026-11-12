package imaged

// adr: 20261112170911 — reuse an app layer whose inputs are unchanged.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// LayerReuseEnv turns on app layer reuse (ADR-20261112170911): a source
// deployment whose layer inputs match an earlier layer of the same account
// republishes that layer instead of converting the image again. Off by
// default.
const LayerReuseEnv = "FAAS_IMAGED_LAYER_REUSE"

// ParseLayerReuse parses a LayerReuseEnv value. Empty is off.
func ParseLayerReuse(raw string) (bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false, nil
	}
	on, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean (got %q)", LayerReuseEnv, raw)
	}
	return on, nil
}

// LayerReuseDir is where imaged keeps its layer reuse index: beside the
// artifact cache's files, which it describes.
func LayerReuseDir(cacheRoot string) string {
	if cacheRoot == "" {
		return ""
	}
	return filepath.Join(cacheRoot, "layer-reuse")
}

// layerReuseFormat versions the key; bump it when Build writes anything new
// into an app layer.
const layerReuseFormat = "app-layer-reuse/v1"

// layerReuseInputs is everything rootfs.Builder.Build writes into an app
// layer, plus the account the layer may be reused in.
type layerReuseInputs struct {
	accountID   string
	imageDigest string
	// runtime, runtimeBaseDiffIDs and runtimeReason describe the shared
	// runner base the layer sits on, when one applies.
	runtime            string
	runtimeBaseDiffIDs []string
	runtimeReason      string
	manifest           api.AppManifest
	guestInitDigest    string
	plan               api.Plan
}

func layerReuseKey(in layerReuseInputs) (string, error) {
	if in.accountID == "" || in.imageDigest == "" || in.guestInitDigest == "" || in.plan == "" {
		return "", errors.New("layer reuse key needs the account, image digest, guest-init digest and plan")
	}
	manifest, err := json.Marshal(in.manifest)
	if err != nil {
		return "", fmt.Errorf("layer reuse key: manifest: %w", err)
	}
	h := sha256.New()
	for _, part := range []string{layerReuseFormat, in.accountID, in.imageDigest, in.runtime,
		strings.Join(in.runtimeBaseDiffIDs, ","), in.runtimeReason, string(manifest), in.guestInitDigest, string(in.plan)} {
		_, _ = fmt.Fprintf(h, "%d:%s\n", len(part), part)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// layerReuseRecord describes a published app layer.
type layerReuseRecord struct {
	AccountID       string `json:"account_id"`
	DeploymentID    string `json:"deployment_id"`
	StorageKey      string `json:"storage_key"`
	ArtifactDigest  string `json:"artifact_digest"`
	ArtifactBytes   int64  `json:"artifact_bytes"`
	ContentBytes    int64  `json:"content_bytes"`
	SizeMB          int    `json:"size_mb"`
	SBOMKey         string `json:"sbom_key,omitempty"`
	GuestInitDigest string `json:"guest_init_digest"`
	RunnerDigest    string `json:"runner_digest,omitempty"`
}

func (r layerReuseRecord) valid() bool {
	return r.AccountID != "" && r.DeploymentID != "" && r.StorageKey != "" &&
		artifactDigestRE.MatchString(r.ArtifactDigest) && r.ArtifactBytes > 0 && r.SizeMB > 0
}

// layerScanSource names the deployment whose scan covers an artifact digest.
type layerScanSource struct {
	AccountID    string `json:"account_id"`
	DeploymentID string `json:"deployment_id"`
}

var (
	layerReuseKeyRE  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	artifactDigestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// layerReuseIndex is imaged's node-local index of published app layers:
// keys/<key>.json holds a layerReuseRecord, digests/<hex>.json the
// layerScanSource for an artifact digest. It is a cache; a missing or
// unreadable file reads as a miss.
type layerReuseIndex struct {
	dir string
	// maxRecords overrides api.ImagedLayerReuseMaxRecords in tests.
	maxRecords int
	mu         sync.Mutex
}

func (x *layerReuseIndex) keyPath(key string) string {
	return filepath.Join(x.dir, "keys", key+".json")
}

func (x *layerReuseIndex) digestPath(digest string) string {
	return filepath.Join(x.dir, "digests", strings.TrimPrefix(digest, "sha256:")+".json")
}

func (x *layerReuseIndex) get(key string) (layerReuseRecord, bool) {
	var rec layerReuseRecord
	if x == nil || !layerReuseKeyRE.MatchString(key) || !x.read(x.keyPath(key), &rec) || !rec.valid() {
		return layerReuseRecord{}, false
	}
	return rec, true
}

// put records rec for key. With scanSource it also names rec's deployment as
// the scan source of its artifact digest, which only a fresh build does.
func (x *layerReuseIndex) put(key string, rec layerReuseRecord, scanSource bool) error {
	if x == nil {
		return nil
	}
	if !layerReuseKeyRE.MatchString(key) || !rec.valid() {
		return errors.New("invalid layer reuse record")
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if err := x.write(x.keyPath(key), rec); err != nil {
		return err
	}
	if scanSource {
		if err := x.write(x.digestPath(rec.ArtifactDigest), layerScanSource{AccountID: rec.AccountID, DeploymentID: rec.DeploymentID}); err != nil {
			return err
		}
	}
	x.pruneLocked("keys")
	x.pruneLocked("digests")
	return nil
}

func (x *layerReuseIndex) drop(key string) {
	if x == nil || !layerReuseKeyRE.MatchString(key) {
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	_ = os.Remove(x.keyPath(key))
}

func (x *layerReuseIndex) scanSource(digest string) (layerScanSource, bool) {
	var src layerScanSource
	if x == nil || !artifactDigestRE.MatchString(digest) || !x.read(x.digestPath(digest), &src) ||
		src.AccountID == "" || src.DeploymentID == "" {
		return layerScanSource{}, false
	}
	return src, true
}

func (x *layerReuseIndex) read(path string, v any) bool {
	//nolint:forbidigo // path is a fixed-shape file under imaged's own index directory.
	data, err := os.ReadFile(path)
	if err != nil || len(data) > api.ImagedLayerReuseRecordMaxBytes {
		return false
	}
	return json.Unmarshal(data, v) == nil
}

// write replaces path atomically.
func (x *layerReuseIndex) write(path string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	_, writeErr := tmp.Write(data)
	if err := errors.Join(writeErr, tmp.Close()); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// pruneLocked keeps the newest api.ImagedLayerReuseMaxRecords files of sub.
func (x *layerReuseIndex) pruneLocked(sub string) {
	limit := api.ImagedLayerReuseMaxRecords
	if x.maxRecords > 0 {
		limit = x.maxRecords
	}
	entries, err := os.ReadDir(filepath.Join(x.dir, sub))
	if err != nil || len(entries) <= limit {
		return
	}
	type aged struct {
		name string
		mod  time.Time
	}
	files := make([]aged, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, aged{e.Name(), info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	for _, f := range files[min(len(files), limit):] {
		_ = os.Remove(filepath.Join(x.dir, sub, f.name))
	}
}

// fileDigests remembers the SHA-256 of a file while its size, modification
// time and inode are unchanged.
type fileDigests struct {
	mu      sync.Mutex
	entries map[string]fileDigest
}

type fileDigest struct {
	info   os.FileInfo
	digest string
}

func (m *fileDigests) digest(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	if e, ok := m.entries[path]; ok && os.SameFile(e.info, info) && e.info.Size() == info.Size() && e.info.ModTime().Equal(info.ModTime()) {
		m.mu.Unlock()
		return e.digest, nil
	}
	m.mu.Unlock()
	//nolint:forbidigo // path is imaged's configured guest-init binary.
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	digest := "sha256:" + hex.EncodeToString(h.Sum(nil))
	m.mu.Lock()
	if m.entries == nil {
		m.entries = map[string]fileDigest{}
	}
	m.entries[path] = fileDigest{info: info, digest: digest}
	m.mu.Unlock()
	return digest, nil
}

// layerRepublisher is a LayerBuilder that can publish an earlier layer's bytes
// under a new key; *rootfs.Builder is one.
type layerRepublisher interface {
	Republish(ctx context.Context, be storage.StorageBackend, key string, src rootfs.PublishedLayer) (rootfs.ArtifactIdentity, error)
}

// WithLayerReuse turns on app layer reuse with its index in dir
// (ADR-20261112170911). An empty dir turns it off. Call before serving.
func (h *Handler) WithLayerReuse(dir string) *Handler {
	h.layerReuse = nil
	if dir != "" {
		h.layerReuse = &layerReuseIndex{dir: dir}
	}
	return h
}

func newLayerReuseMetric(reg prometheus.Registerer) *prometheus.CounterVec {
	m := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "imaged_app_layer_reuse_total",
		Help: "App layer reuse decisions (ADR-20261112170911) by outcome: hit republished an earlier layer; miss found none; source_gone, failed and mismatch fell back to a build.",
	}, []string{"outcome"})
	for _, outcome := range []string{"hit", "miss", "source_gone", "failed", "mismatch"} {
		m.WithLabelValues(outcome)
	}
	if err := reg.Register(m); err != nil {
		var already prometheus.AlreadyRegisteredError
		if errors.As(err, &already) {
			if existing, ok := already.ExistingCollector.(*prometheus.CounterVec); ok {
				return existing
			}
		}
		return nil
	}
	return m
}

func (h *Handler) observeLayerReuse(outcome string) {
	if h.layerReuseMetric != nil {
		h.layerReuseMetric.WithLabelValues(outcome).Inc()
	}
}

// appLayerReuseKey returns the layer input key of an eligible deployment, or
// "" when reuse is off or the inputs cannot be identified.
func (h *Handler) appLayerReuseKey(app state.App, acct state.Account, imageDigest string, shared *sharedRuntimeLayers, runtime string, manifest api.AppManifest) string {
	if h.layerReuse == nil {
		return ""
	}
	guestInit, err := h.guestInitDigests.digest(h.guestInitPath)
	if err != nil {
		h.log.Warn("imaged: layer reuse: guest-init digest", "err", err)
		return ""
	}
	in := layerReuseInputs{accountID: app.AccountID, imageDigest: imageDigest, runtime: runtime,
		manifest: manifest, guestInitDigest: guestInit, plan: acct.Plan}
	if shared != nil {
		in.runtimeBaseDiffIDs, in.runtimeReason = shared.baseDiffIDs, shared.Reason
	}
	key, err := layerReuseKey(in)
	if err != nil {
		h.log.Warn("imaged: layer reuse key", "app", app.Slug, "err", err)
		return ""
	}
	return key
}

// buildOrReuseAppLayer republishes the layer recorded for key when it can,
// and otherwise builds it and records it for key.
func (h *Handler) buildOrReuseAppLayer(ctx context.Context, key string, app state.App, dep state.Deployment, in rootfs.BuildInput) (rootfs.BuildResult, error) {
	if key != "" {
		if result, ok := h.reuseAppLayer(ctx, key, app, dep, in); ok {
			return result, nil
		}
	}
	result, err := h.buildAppLayer(ctx, in)
	if err == nil && key != "" {
		h.recordAppLayer(key, app, dep, result, true)
	}
	return result, err
}

func (h *Handler) reuseAppLayer(ctx context.Context, key string, app state.App, dep state.Deployment, in rootfs.BuildInput) (rootfs.BuildResult, bool) {
	rec, ok := h.layerReuse.get(key)
	if !ok {
		h.observeLayerReuse("miss")
		return rootfs.BuildResult{}, false
	}
	republisher, ok := h.builder.(layerRepublisher)
	if !ok || in.Storage == nil || in.StorageKey == "" {
		return rootfs.BuildResult{}, false
	}
	if rec.AccountID != app.AccountID || rec.DeploymentID == dep.ID || rec.StorageKey == in.StorageKey {
		h.layerReuse.drop(key)
		h.observeLayerReuse("mismatch")
		return rootfs.BuildResult{}, false
	}
	src, ok := localStoragePath(in.Storage, rec.StorageKey)
	if !ok {
		h.layerReuse.drop(key)
		h.observeLayerReuse("source_gone")
		return rootfs.BuildResult{}, false
	}
	start := time.Now()
	identity, err := republisher.Republish(ctx, in.Storage, in.StorageKey, rootfs.PublishedLayer{Path: src, Digest: rec.ArtifactDigest, Bytes: rec.ArtifactBytes})
	if err != nil {
		h.layerReuse.drop(key)
		h.observeLayerReuse("failed")
		h.log.Warn("imaged: layer reuse failed; building the layer", "app", app.Slug, "deployment", dep.ID, "source_deployment", rec.DeploymentID, "err", err)
		return rootfs.BuildResult{}, false
	}
	h.layerDigests.record(in.Storage, in.StorageKey, identity.Digest)
	result := rootfs.BuildResult{
		ImageKey: in.StorageKey, SizeMB: rec.SizeMB, ContentBytes: rec.ContentBytes,
		ArtifactDigest: identity.Digest, ArtifactBytes: identity.Bytes,
		SBOMKey:      h.copySBOM(ctx, in.Storage, rec.SBOMKey, in.SBOMStorageKey),
		RunnerDigest: rec.RunnerDigest, GuestInitDigest: rec.GuestInitDigest,
	}
	// The newest copy is the likeliest to stay cached and uncollected. The
	// scan source stays the deployment that was scanned.
	h.recordAppLayer(key, app, dep, result, false)
	h.observeLayerReuse("hit")
	h.log.Info("imaged: reused app layer", "app", app.Slug, "deployment", dep.ID,
		"source_deployment", rec.DeploymentID, "bytes", identity.Bytes, "ms", time.Since(start).Milliseconds())
	return result, true
}

func (h *Handler) recordAppLayer(key string, app state.App, dep state.Deployment, result rootfs.BuildResult, scanSource bool) {
	rec := layerReuseRecord{AccountID: app.AccountID, DeploymentID: dep.ID, StorageKey: result.ImageKey,
		ArtifactDigest: result.ArtifactDigest, ArtifactBytes: result.ArtifactBytes, ContentBytes: result.ContentBytes,
		SizeMB: result.SizeMB, SBOMKey: result.SBOMKey, GuestInitDigest: result.GuestInitDigest, RunnerDigest: result.RunnerDigest}
	if !rec.valid() {
		return
	}
	if err := h.layerReuse.put(key, rec, scanSource); err != nil {
		h.log.Warn("imaged: record app layer for reuse", "app", app.Slug, "deployment", dep.ID, "err", err)
	}
}

// copySBOM copies the source layer's SBOM to the new deployment's SBOM key.
// Like the SBOM a build emits, it is best effort: "" when there is none.
func (h *Handler) copySBOM(ctx context.Context, be storage.StorageBackend, src, dst string) string {
	if src == "" || dst == "" {
		return ""
	}
	rc, err := be.Get(ctx, src)
	if err != nil {
		return ""
	}
	defer func() { _ = rc.Close() }()
	if err := be.Put(ctx, dst, &cappedReader{r: rc, left: api.ImagedLayerReuseSBOMMaxBytes}); err != nil {
		h.log.Warn("imaged: copy SBOM for reused layer", "src", src, "dst", dst, "err", err)
		return ""
	}
	return dst
}

// cappedReader fails a read past left bytes instead of truncating, so an
// oversized source is never stored as a cut-off copy.
type cappedReader struct {
	r    io.Reader
	left int64
}

func (c *cappedReader) Read(p []byte) (int, error) {
	if c.left <= 0 {
		var probe [1]byte
		if n, _ := c.r.Read(probe[:]); n > 0 {
			return 0, errors.New("imaged: SBOM exceeds the copy limit")
		}
		return 0, io.EOF
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	return n, err
}

// localStoragePath is be's local file for key, when it keeps one.
func localStoragePath(be storage.StorageBackend, key string) (string, bool) {
	resolver, ok := be.(storage.LocalPathResolver)
	if !ok {
		return "", false
	}
	path, local, err := resolver.LocalPath(key)
	if err != nil || !local {
		return "", false
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return path, true
}

// layerScanSourceResult is the scan of the deployment the layer reuse index
// names for artifactDigest, when it is in app's account and reusable under
// the per-deploy rule.
func (h *Handler) layerScanSourceResult(ctx context.Context, app state.App, dep state.Deployment, artifactDigest string, scanner deployScanner) *ScanResult {
	src, ok := h.layerReuse.scanSource(artifactDigest)
	if !ok || src.AccountID != app.AccountID || src.DeploymentID == dep.ID {
		return nil
	}
	prior, err := h.store.DeploymentByID(ctx, src.DeploymentID)
	if err != nil {
		return nil
	}
	owner, err := state.AppForDeployment(ctx, h.store, prior)
	if err != nil || owner.AccountID != app.AccountID {
		return nil
	}
	return reusableScanOf(prior, artifactDigest, scanner)
}
