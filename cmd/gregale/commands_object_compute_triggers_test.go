package main_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

type fakeManifestComputeTriggers struct {
	*fakeManifestObjectStorageClient
	requests []api.ReconcileObjectStorageComputeTriggersRequest
	targets  []string
	reads    int
	err      error
}

func (f *fakeManifestComputeTriggers) GetObjectBucketNotifications(context.Context, string, string) (api.ObjectBucketNotifications, error) {
	f.reads++
	return api.ObjectBucketNotifications{Revision: 7}, nil
}
func (f *fakeManifestComputeTriggers) ReconcileObjectStorageComputeTriggers(_ context.Context, slug, bucket string, r api.ReconcileObjectStorageComputeTriggersRequest) (api.ObjectBucketNotifications, error) {
	f.requests = append(f.requests, r)
	f.targets = append(f.targets, slug+"/"+bucket)
	return api.ObjectBucketNotifications{}, f.err
}

// adr: 1036
func TestManifestComputeTriggers(t *testing.T) {
	previous := (*main.JsonOutput)
	(*main.JsonOutput) = true
	t.Cleanup(func() { (*main.JsonOutput) = previous })
	for _, tc := range []struct {
		name, decl string
		specified  bool
		count      int
	}{
		{"omitted", "", false, 0}, {"clear", "    triggers: []\n", true, 0}, {"configured", "    triggers:\n      - id: images\n        prefix: uploads/\n", true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			main.WriteManifest(t, dir, "buckets:\n  - bucket: assets\n    app: worker\n    scope: production\n"+tc.decl)
			f := &fakeManifestComputeTriggers{fakeManifestObjectStorageClient: &fakeManifestObjectStorageClient{buckets: api.ObjectBucketList{Items: []api.ObjectBucket{{ID: "bucket", Name: "assets", Scope: "production", State: "ready"}}}, bindings: map[string]api.ObjectStorageComputeBindingList{"bucket": {Items: []api.ObjectStorageComputeBinding{{Prefix: "GREGALE_S3_ASSETS", Scope: "production"}}}}}}
			if err := main.DeployManifestObjectStorageBindings(t.Context(), f, "worker", dir); err != nil {
				t.Fatal(err)
			}
			if (f.reads > 0) != tc.specified || len(f.fakeManifestObjectStorageClient.requests) != 0 {
				t.Fatal("unexpected mutation", f)
			}
			if tc.specified && (len(f.requests) != 1 || *f.requests[0].ExpectedRevision != 7 || f.requests[0].Triggers == nil || len(f.requests[0].Triggers) != tc.count || f.targets[0] != "worker/bucket") {
				t.Fatal(f.requests, f.targets)
			}
			if tc.specified {
				f.err = errors.New("revision conflict")
				if main.DeployManifestObjectStorageBindings(t.Context(), f, "worker", dir) == nil {
					t.Fatal("conflict ignored")
				}
			}
		})
	}
}
func TestManifestComputeTriggersPreflight(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		dir := t.TempDir()
		manifest := "buckets:\n  - bucket: assets\n    prefix: FIRST\n    triggers: []\n"
		if duplicate {
			manifest += "  - bucket: bucket\n    prefix: SECOND\n    triggers: []\n"
		}
		main.WriteManifest(t, dir, manifest)
		base := &fakeManifestObjectStorageClient{buckets: api.ObjectBucketList{Items: []api.ObjectBucket{{ID: "bucket", Name: "assets", Scope: "default", State: "ready"}}}}
		var f main.ManifestObjectStorageClient = base
		if duplicate {
			f = &fakeManifestComputeTriggers{fakeManifestObjectStorageClient: base}
		}
		if main.DeployManifestObjectStorageBindings(context.Background(), f, "worker", dir) == nil || len(base.requests) != 0 {
			t.Fatal("unsafe preflight", duplicate)
		}
	}
}
