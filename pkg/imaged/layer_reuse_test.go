package imaged

// adr: 20261112170911

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

func TestParseLayerReuse(t *testing.T) {
	for raw, want := range map[string]bool{"": false, " ": false, "1": true, "true": true, "0": false, "false": false} {
		if got, err := ParseLayerReuse(raw); err != nil || got != want {
			t.Errorf("ParseLayerReuse(%q) = %v, %v; want %v", raw, got, err, want)
		}
	}
	if _, err := ParseLayerReuse("yes"); err == nil {
		t.Error("ParseLayerReuse accepted a non-boolean")
	}
}

func TestLayerReuseKeyCoversEveryLayerInput(t *testing.T) {
	base := layerReuseInputs{
		accountID: "acct", imageDigest: "sha256:img", manifest: api.AppManifest{Entrypoint: []string{"node", "index.js"}, Port: 8080},
		guestInitDigest: "sha256:init", plan: api.PlanPro,
	}
	baseKey, err := layerReuseKey(base)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := layerReuseKey(base); again != baseKey {
		t.Fatal("key not stable")
	}
	changes := map[string]func(*layerReuseInputs){
		"account":           func(in *layerReuseInputs) { in.accountID = "other" },
		"image":             func(in *layerReuseInputs) { in.imageDigest = "sha256:img2" },
		"shared runtime":    func(in *layerReuseInputs) { in.runtime = "node24" },
		"runner base":       func(in *layerReuseInputs) { in.runtimeBaseDiffIDs = []string{"sha256:l1"} },
		"split outcome":     func(in *layerReuseInputs) { in.runtimeReason = "content_outside_app" },
		"manifest command":  func(in *layerReuseInputs) { in.manifest.Entrypoint = []string{"node", "server.js"} },
		"manifest env":      func(in *layerReuseInputs) { in.manifest.Env = map[string]string{"A": "1"} },
		"guest-init binary": func(in *layerReuseInputs) { in.guestInitDigest = "sha256:init2" },
		"plan":              func(in *layerReuseInputs) { in.plan = api.PlanHobby },
	}
	for name, change := range changes {
		in := base
		change(&in)
		key, err := layerReuseKey(in)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if key == baseKey {
			t.Errorf("changing the %s keeps the key", name)
		}
	}
	for _, missing := range []func(*layerReuseInputs){
		func(in *layerReuseInputs) { in.accountID = "" },
		func(in *layerReuseInputs) { in.imageDigest = "" },
		func(in *layerReuseInputs) { in.guestInitDigest = "" },
		func(in *layerReuseInputs) { in.plan = "" },
	} {
		in := base
		missing(&in)
		if _, err := layerReuseKey(in); err == nil {
			t.Error("layerReuseKey accepted inputs it cannot identify")
		}
	}
}

func testLayerRecord(dep string) layerReuseRecord {
	return layerReuseRecord{AccountID: "acct", DeploymentID: dep, StorageKey: "apps/a/" + dep + ".ext4",
		ArtifactDigest: "sha256:" + strings.Repeat("b", 64), ArtifactBytes: 10, SizeMB: 64}
}

func TestLayerReuseIndex(t *testing.T) {
	x := &layerReuseIndex{dir: t.TempDir(), maxRecords: 2}
	key := strings.Repeat("a", 64)
	if _, ok := x.get(key); ok {
		t.Fatal("empty index has a record")
	}
	if err := x.put("not-a-key", testLayerRecord("d1"), true); err == nil {
		t.Fatal("put accepted a malformed key")
	}
	if err := x.put(key, layerReuseRecord{AccountID: "acct"}, true); err == nil {
		t.Fatal("put accepted an incomplete record")
	}
	if err := x.put(key, testLayerRecord("d1"), true); err != nil {
		t.Fatal(err)
	}
	if rec, ok := x.get(key); !ok || rec.DeploymentID != "d1" {
		t.Fatalf("get = %+v, %v", rec, ok)
	}
	if src, ok := x.scanSource(testLayerRecord("d1").ArtifactDigest); !ok || src.DeploymentID != "d1" || src.AccountID != "acct" {
		t.Fatalf("scan source = %+v, %v", src, ok)
	}
	// A reuse moves the record but keeps the scanned deployment as the source.
	if err := x.put(key, testLayerRecord("d2"), false); err != nil {
		t.Fatal(err)
	}
	if rec, _ := x.get(key); rec.DeploymentID != "d2" {
		t.Fatalf("record after reuse names %s", rec.DeploymentID)
	}
	if src, _ := x.scanSource(testLayerRecord("d1").ArtifactDigest); src.DeploymentID != "d1" {
		t.Fatalf("scan source after reuse = %s, want the scanned deployment", src.DeploymentID)
	}
	x.drop(key)
	if _, ok := x.get(key); ok {
		t.Fatal("drop kept the record")
	}
	for i, k := range []string{strings.Repeat("1", 64), strings.Repeat("2", 64), strings.Repeat("3", 64)} {
		if err := x.put(k, testLayerRecord("p"+string(rune('a'+i))), false); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	entries, err := os.ReadDir(filepath.Join(x.dir, "keys"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("index keeps %d records (%v), want the newest 2", len(entries), err)
	}
	if _, ok := x.get(strings.Repeat("1", 64)); ok {
		t.Fatal("pruning kept the oldest record")
	}
}

// reuseBuilder is a fakeBuilder that reports a filesystem size and
// republishes with the real rootfs logic.
type reuseBuilder struct {
	*fakeBuilder
	republishes int
}

func (b *reuseBuilder) Build(ctx context.Context, in rootfs.BuildInput) (rootfs.BuildResult, error) {
	res, err := b.fakeBuilder.Build(ctx, in)
	res.SizeMB = 64
	return res, err
}

func (b *reuseBuilder) Republish(ctx context.Context, be storage.StorageBackend, key string, src rootfs.PublishedLayer) (rootfs.ArtifactIdentity, error) {
	b.republishes++
	return rootfs.NewBuilder(nil).Republish(ctx, be, key, src)
}

func TestBuildOrReuseAppLayerRepublishesARecordedLayer(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	be, err := storage.NewLocalStorageBackend(root)
	if err != nil {
		t.Fatal(err)
	}
	builder := &reuseBuilder{fakeBuilder: &fakeBuilder{}}
	h := New(state.NewMemStore(), &fakeNotifier{}, fakePuller{}, builder, "./init", root, slog.New(slog.DiscardHandler)).
		WithStorage(be).WithLayerReuse(filepath.Join(t.TempDir(), "layer-reuse"))
	app := state.App{ID: "app-1", AccountID: "acct-1", Slug: "a"}
	key := strings.Repeat("c", 64)
	deploy := func(app state.App, dep string) rootfs.BuildResult {
		t.Helper()
		res, err := h.buildOrReuseAppLayer(ctx, key, app, state.Deployment{ID: dep}, rootfs.BuildInput{Storage: be, StorageKey: "apps/a/" + dep + ".ext4"})
		if err != nil {
			t.Fatalf("deploy %s: %v", dep, err)
		}
		return res
	}

	first := deploy(app, "dep-1")
	second := deploy(app, "dep-2")
	if len(builder.calls) != 1 || builder.republishes != 1 {
		t.Fatalf("builds %d, republishes %d; want the second deploy to republish", len(builder.calls), builder.republishes)
	}
	if second.ImageKey != "apps/a/dep-2.ext4" || second.ArtifactDigest != first.ArtifactDigest || second.ArtifactBytes != first.ArtifactBytes || second.SizeMB != first.SizeMB {
		t.Fatalf("reused result %+v does not match the built %+v", second, first)
	}
	rc, err := be.Get(ctx, "apps/a/dep-2.ext4")
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(stored) != "fake ext4" {
		t.Fatalf("republished %q", stored)
	}
	if src, ok := h.layerReuse.scanSource(first.ArtifactDigest); !ok || src.DeploymentID != "dep-1" {
		t.Fatalf("scan source = %+v, %v; want the built deployment", src, ok)
	}

	// The record now names dep-2. With its layer gone, the next deploy builds.
	if err := be.Delete(ctx, "apps/a/dep-2.ext4"); err != nil {
		t.Fatal(err)
	}
	deploy(app, "dep-3")
	if len(builder.calls) != 2 {
		t.Fatalf("builds %d after the recorded layer was collected, want a rebuild", len(builder.calls))
	}

	// A record of another account is never used.
	other := app
	other.AccountID = "acct-2"
	deploy(other, "dep-4")
	if len(builder.calls) != 3 || builder.republishes != 1 {
		t.Fatalf("builds %d, republishes %d; another account's record was used", len(builder.calls), builder.republishes)
	}

	// With reuse off, nothing is looked up or recorded.
	h.WithLayerReuse("")
	if _, err := h.buildOrReuseAppLayer(ctx, "", app, state.Deployment{ID: "dep-5"}, rootfs.BuildInput{Storage: be, StorageKey: "apps/a/dep-5.ext4"}); err != nil {
		t.Fatal(err)
	}
	if len(builder.calls) != 4 {
		t.Fatal("reuse off did not build")
	}
}

// A layer copied from another app's deployment of the account reuses that
// deployment's scan; one of another account does not.
func TestDeployScanReusesTheLayerSourcesScan(t *testing.T) {
	cases := []struct {
		name        string
		sameAccount bool
		wantGrype   int
	}{
		{name: "same account", sameAccount: true, wantGrype: 1},
		{name: "other account", sameAccount: false, wantGrype: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			appsRoot := t.TempDir()
			grypeRuns := 0
			stubGrype := func(context.Context, string) (*ScanResult, error) {
				grypeRuns++
				return &ScanResult{ScannerVersion: "0.116.0", ScannerDBStatus: "valid",
					ScannerDBVersion: "v6.1.2", ScannerDBBuiltAt: "2026-10-28T00:00:00Z"}, nil
			}
			backend, err := storage.NewLocalStorageBackend(appsRoot)
			if err != nil {
				t.Fatal(err)
			}
			h := New(state.NewMemStore(), &fakeNotifier{}, fakePuller{}, &fakeBuilder{}, "./init", appsRoot, slog.New(slog.DiscardHandler)).
				WithStorage(backend).WithGrypeRun(stubGrype).WithLayerReuse(filepath.Join(t.TempDir(), "layer-reuse"))
			ctx := context.Background()
			newApp := func(email, slug string) state.App {
				t.Helper()
				acct, err := h.store.CreateAccount(ctx, email, api.PlanPro)
				if err != nil {
					t.Fatal(err)
				}
				app, err := h.store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, RAMMB: 512, IdleTimeoutS: 60, MaxConcurrency: 5})
				if err != nil {
					t.Fatal(err)
				}
				return app
			}
			deploy := func(app state.App) state.Deployment {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(appsRoot, app.Slug), 0o755); err != nil {
					t.Fatal(err)
				}
				dep, err := h.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:abc", Kind: state.DeploymentKindImage})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(appsRoot, app.Slug, dep.ID+".ext4"), []byte("layer"), 0o600); err != nil {
					t.Fatal(err)
				}
				_ = h.runDeployScan(ctx, app, dep)
				dep, err = h.store.DeploymentByID(ctx, dep.ID)
				if err != nil {
					t.Fatal(err)
				}
				return dep
			}
			source := newApp("source@example.com", "source")
			first := deploy(source)
			evidence := decodeScanEvidence(first.ScanResult)
			if evidence == nil {
				t.Fatalf("source scan = %s", first.ScanResult)
			}
			if err := h.layerReuse.put(strings.Repeat("d", 64), layerReuseRecord{AccountID: source.AccountID, DeploymentID: first.ID,
				StorageKey: "apps/source/" + first.ID + ".ext4", ArtifactDigest: evidence.ArtifactDigest, ArtifactBytes: 5, SizeMB: 64}, true); err != nil {
				t.Fatal(err)
			}
			var target state.App
			if tc.sameAccount {
				var err error
				target, err = h.store.CreateApp(ctx, state.App{AccountID: source.AccountID, Slug: "target", RAMMB: 512, IdleTimeoutS: 60, MaxConcurrency: 5})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				target = newApp("other@example.com", "target")
			}
			second := deploy(target)
			if grypeRuns != tc.wantGrype {
				t.Fatalf("grype ran %d times, want %d", grypeRuns, tc.wantGrype)
			}
			got := decodeScanEvidence(second.ScanResult)
			if reused := got != nil && got.ReusedFrom == first.ID; reused != tc.sameAccount {
				t.Fatalf("reused_from = %+v, want reuse %v", got, tc.sameAccount)
			}
		})
	}
}
