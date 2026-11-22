package secretd_test

// adr: 20261117190000

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/secretd"
	"github.com/onebox-faas/faas/pkg/storagegrant"
)

const (
	depA     = "0b8f5a8e-7c1e-4f3a-9d2b-1a2b3c4d5e6f" // a deployment of app-a (owned by owner-node)
	depB     = "1b8f5a8e-7c1e-4f3a-9d2b-1a2b3c4d5e6f" // a deployment of app-b (owned by other-node)
	acctS    = "11111111-2222-4333-8444-555555555555"
	jobJ     = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	captureC = "c0ffee00-0000-4000-8000-000000000001"
)

type fakeStorageStore struct {
	deployments map[string]string
	runsApp     map[[2]string]bool // {app, node id}
	builds      map[[2]string]bool // {deployment, node name}
	sandboxes   map[[2]string]bool // {account, node id}
	jobs        map[[2]string]bool // {job, node id}
	templates   map[[2]string]bool // {app, node id}
	records     []secretd.StorageGrantRecord
	failRecord  bool
}

func (f *fakeStorageStore) DeploymentApp(_ context.Context, id string) (string, error) {
	if app, ok := f.deployments[id]; ok {
		return app, nil
	}
	return "", secretd.ErrUnknownDeployment
}
func (f *fakeStorageStore) NodeRunsApp(_ context.Context, app, node string) (bool, error) {
	return f.runsApp[[2]string{app, node}], nil
}
func (f *fakeStorageStore) NodeBuildsDeployment(_ context.Context, dep, name string, _ time.Time) (bool, error) {
	return f.builds[[2]string{dep, name}], nil
}
func (f *fakeStorageStore) NodeRunsSandboxOf(_ context.Context, account, node string) (bool, error) {
	return f.sandboxes[[2]string{account, node}], nil
}
func (f *fakeStorageStore) NodeRunsSandboxTemplateOf(_ context.Context, app, node string) (bool, error) {
	return f.templates[[2]string{app, node}], nil
}
func (f *fakeStorageStore) NodeRunsJob(_ context.Context, job, node string) (bool, error) {
	return f.jobs[[2]string{job, node}], nil
}
func (f *fakeStorageStore) RecordStorageGrant(_ context.Context, r secretd.StorageGrantRecord) error {
	if f.failRecord {
		return errors.New("db down")
	}
	f.records = append(f.records, r)
	return nil
}

type fakeSigner struct{ signed []storagegrant.Request }

func (s *fakeSigner) Sign(_ context.Context, r storagegrant.Request, expires time.Time) (storagegrant.Grant, error) {
	if r.Op == storagegrant.OpReplace && r.Key == "layers/"+depA+".ext4" {
		return storagegrant.Grant{}, storagegrant.ErrUnsupported
	}
	s.signed = append(s.signed, r)
	return storagegrant.Grant{Method: "GET", URL: "https://store.test/" + r.Key, Expires: expires}, nil
}

func storageFixture(t *testing.T) (*fixture, *fakeStorageStore, *fakeSigner) {
	t.Helper()
	f := newFixture(t, 0)
	st := &fakeStorageStore{
		deployments: map[string]string{depA: "app-a", depB: "app-b"},
		runsApp:     map[[2]string]bool{}, builds: map[[2]string]bool{},
		sandboxes: map[[2]string]bool{}, jobs: map[[2]string]bool{}, templates: map[[2]string]bool{},
	}
	sig := &fakeSigner{}
	f.server.WithStorageGrants(st, sig)
	return f, st, sig
}

func grant(f *fixture, objs ...*secretdpb.StorageObjectRequest) (*secretdpb.GrantStorageObjectsResponse, codes.Code) {
	resp, err := f.server.GrantStorageObjects(f.ctx, &secretdpb.GrantStorageObjectsRequest{Objects: objs})
	return resp, status.Code(err)
}

func get(key string) *secretdpb.StorageObjectRequest {
	return &secretdpb.StorageObjectRequest{Key: key, Op: "get"}
}

func create(key string) *secretdpb.StorageObjectRequest {
	return &secretdpb.StorageObjectRequest{Key: key, Op: "create"}
}

func TestGrantStorageObjects_PlacementDecides(t *testing.T) {
	snapA := "snap/" + depA + "/captures/" + captureC + "/v2/mem"
	snapB := "snap/" + depB + "/captures/" + captureC + "/v2/mem"
	for name, tc := range map[string]struct {
		caller string
		setup  func(*fakeStorageStore, *fixture)
		objs   []*secretdpb.StorageObjectRequest
		want   codes.Code
	}{
		"owner reads and writes its app":    {caller: "owner-node", objs: []*secretdpb.StorageObjectRequest{get(snapA), create(snapA)}, want: codes.OK},
		"owner reads another app":           {caller: "owner-node", objs: []*secretdpb.StorageObjectRequest{get(snapB)}, want: codes.PermissionDenied},
		"one foreign key refuses the batch": {caller: "owner-node", objs: []*secretdpb.StorageObjectRequest{get(snapA), get(snapB)}, want: codes.PermissionDenied},
		"granted peer": {caller: "peer-node", setup: func(_ *fakeStorageStore, f *fixture) {
			f.store.grants[grantKey{"app-a", "n-peer"}] = f.now.Add(time.Hour)
		}, objs: []*secretdpb.StorageObjectRequest{get(snapA)}, want: codes.OK},
		"node with a live instance": {caller: "peer-node", setup: func(s *fakeStorageStore, _ *fixture) { s.runsApp[[2]string{"app-a", "n-peer"}] = true }, objs: []*secretdpb.StorageObjectRequest{get(snapA)}, want: codes.OK},
		"node preparing the image": {caller: "peer-node", setup: func(_ *fakeStorageStore, f *fixture) {
			f.store.preparing = map[[2]string]bool{{"app-a", "peer-node"}: true}
		}, objs: []*secretdpb.StorageObjectRequest{create("apps/shop/" + depA + ".ext4")}, want: codes.OK},
		"node building the deployment":    {caller: "peer-node", setup: func(s *fakeStorageStore, _ *fixture) { s.builds[[2]string{depA, "peer-node"}] = true }, objs: []*secretdpb.StorageObjectRequest{create("layers/" + depA + ".ext4")}, want: codes.OK},
		"unknown deployment":              {caller: "owner-node", objs: []*secretdpb.StorageObjectRequest{get("layers/22222222-2222-4222-8222-222222222222.ext4")}, want: codes.PermissionDenied},
		"platform read anywhere":          {caller: "other-node", objs: []*secretdpb.StorageObjectRequest{get("base/base-amd64.ext4"), get("kernel/1.12.0")}, want: codes.OK},
		"platform create anywhere":        {caller: "other-node", objs: []*secretdpb.StorageObjectRequest{create("base/base-amd64.ext4"), create("sigs/base/base-amd64.ext4.sig"), create("scans/base-amd64.ext4.scan.json")}, want: codes.OK},
		"platform conditioned replace":    {caller: "other-node", objs: []*secretdpb.StorageObjectRequest{{Key: "base/base-amd64.ext4", Op: "replace", Generation: 3}, {Key: "scans/base-amd64.ext4.scan.json", Op: "replace", Etag: `"e"`}}, want: codes.OK},
		"platform conditioned complete":   {caller: "other-node", objs: []*secretdpb.StorageObjectRequest{{Key: "base/base-amd64.ext4", Op: "complete_multipart", UploadId: "u", Etag: `"e"`}}, want: codes.OK},
		"platform unconditioned replace":  {caller: "owner-node", objs: []*secretdpb.StorageObjectRequest{{Key: "base/base-amd64.ext4", Op: "replace"}}, want: codes.InvalidArgument},
		"layer signature follows its app": {caller: "owner-node", objs: []*secretdpb.StorageObjectRequest{create("sigs/layers/" + depA + ".ext4.sig")}, want: codes.OK},
		"another app's signature":         {caller: "owner-node", objs: []*secretdpb.StorageObjectRequest{get("sigs/layers/" + depB + ".ext4.sig")}, want: codes.PermissionDenied},
		"builder creates its sbom": {caller: "peer-node", setup: func(_ *fakeStorageStore, f *fixture) {
			f.store.builds = map[string]secretd.BuildRun{jobJ: {AppID: "app-a", Running: false, StartedAt: f.now, Node: "peer-node"}}
		}, objs: []*secretdpb.StorageObjectRequest{create("sboms/" + jobJ + ".cdx.json")}, want: codes.OK},
		"another node's sbom": {caller: "other-node", setup: func(_ *fakeStorageStore, f *fixture) {
			f.store.builds = map[string]secretd.BuildRun{jobJ: {AppID: "app-a", Running: true, StartedAt: f.now, Node: "peer-node"}}
		}, objs: []*secretdpb.StorageObjectRequest{create("sboms/" + jobJ + ".cdx.json")}, want: codes.PermissionDenied},
		"sbom never replaced": {caller: "peer-node", setup: func(_ *fakeStorageStore, f *fixture) {
			f.store.builds = map[string]secretd.BuildRun{jobJ: {AppID: "app-a", Running: true, StartedAt: f.now, Node: "peer-node"}}
		}, objs: []*secretdpb.StorageObjectRequest{{Key: "sboms/" + jobJ + ".cdx.json", Op: "replace", Generation: 2}}, want: codes.PermissionDenied},
		"sandbox session of the account": {caller: "peer-node", setup: func(s *fakeStorageStore, _ *fixture) { s.sandboxes[[2]string{acctS, "n-peer"}] = true }, objs: []*secretdpb.StorageObjectRequest{get("snap/sandbox-template-" + acctS + "/captures/" + captureC + "/v2/mem")}, want: codes.OK},
		"no sandbox session":             {caller: "peer-node", objs: []*secretdpb.StorageObjectRequest{get("snap/sandbox-template-" + acctS + "/captures/" + captureC + "/v2/mem")}, want: codes.PermissionDenied},
		"job lease holder": {caller: "peer-node", setup: func(_ *fakeStorageStore, f *fixture) {
			f.store.jobLeases = map[string]fakeJobLease{jobJ: {owner: "peer-node/claim", until: f.now.Add(time.Minute)}}
		}, objs: []*secretdpb.StorageObjectRequest{create("jobs/" + jobJ + ".ext4")}, want: codes.OK},
		"expired job lease": {caller: "peer-node", setup: func(_ *fakeStorageStore, f *fixture) {
			f.store.jobLeases = map[string]fakeJobLease{jobJ: {owner: "peer-node/claim", until: f.now.Add(-time.Minute)}}
		}, objs: []*secretdpb.StorageObjectRequest{get("jobs/" + jobJ + ".ext4")}, want: codes.PermissionDenied},
		"builder reads its source": {caller: "peer-node", setup: func(_ *fakeStorageStore, f *fixture) {
			f.store.builds = map[string]secretd.BuildRun{jobJ: {AppID: "app-a", Running: true, StartedAt: f.now, Node: "peer-node"}}
		}, objs: []*secretdpb.StorageObjectRequest{get("sources/" + jobJ + ".tar.gz")}, want: codes.OK},
		"another node's build source": {caller: "other-node", setup: func(_ *fakeStorageStore, f *fixture) {
			f.store.builds = map[string]secretd.BuildRun{jobJ: {AppID: "app-a", Running: true, StartedAt: f.now, Node: "peer-node"}}
		}, objs: []*secretdpb.StorageObjectRequest{get("sources/" + jobJ + ".tar.gz")}, want: codes.PermissionDenied},
		"no node writes sources": {caller: "peer-node", setup: func(_ *fakeStorageStore, f *fixture) {
			f.store.builds = map[string]secretd.BuildRun{jobJ: {AppID: "app-a", Running: true, StartedAt: f.now, Node: "peer-node"}}
		}, objs: []*secretdpb.StorageObjectRequest{create("sources/" + jobJ + ".tar.gz")}, want: codes.PermissionDenied},
		"session reads its template layer":    {caller: "peer-node", setup: func(s *fakeStorageStore, _ *fixture) { s.templates[[2]string{"app-a", "n-peer"}] = true }, objs: []*secretdpb.StorageObjectRequest{get("apps/tpl/" + depA + ".ext4")}, want: codes.OK},
		"session cannot write the template":   {caller: "peer-node", setup: func(s *fakeStorageStore, _ *fixture) { s.templates[[2]string{"app-a", "n-peer"}] = true }, objs: []*secretdpb.StorageObjectRequest{create("apps/tpl/" + depA + ".ext4")}, want: codes.PermissionDenied},
		"job task on the node":                {caller: "peer-node", setup: func(s *fakeStorageStore, _ *fixture) { s.jobs[[2]string{jobJ, "n-peer"}] = true }, objs: []*secretdpb.StorageObjectRequest{get("jobs/" + jobJ + ".ext4")}, want: codes.OK},
		"unknown key class":                   {caller: "owner-node", objs: []*secretdpb.StorageObjectRequest{get("sbom/x.json")}, want: codes.InvalidArgument},
		"delete is not an operation":          {caller: "owner-node", objs: []*secretdpb.StorageObjectRequest{{Key: snapA, Op: "delete"}}, want: codes.InvalidArgument},
		"unknown node":                        {caller: "stranger", objs: []*secretdpb.StorageObjectRequest{get("base/base-amd64.ext4")}, want: codes.PermissionDenied},
		"unsupported condition on this store": {caller: "owner-node", objs: []*secretdpb.StorageObjectRequest{{Key: "layers/" + depA + ".ext4", Op: "replace", Generation: 3}}, want: codes.FailedPrecondition},
	} {
		t.Run(name, func(t *testing.T) {
			f, st, _ := storageFixture(t)
			f.caller = tc.caller
			if tc.setup != nil {
				tc.setup(st, f)
			}
			resp, code := grant(f, tc.objs...)
			if code != tc.want {
				t.Fatalf("code = %v, want %v", code, tc.want)
			}
			if code == codes.OK && len(resp.GetGrants()) != len(tc.objs) {
				t.Fatalf("got %d grants for %d objects", len(resp.GetGrants()), len(tc.objs))
			}
			if code == codes.PermissionDenied && len(st.records) != 0 {
				t.Fatalf("a refused request was audited: %+v", st.records)
			}
		})
	}
}

// Grants come back in request order, expire after the TTL, and each tenant
// subject is audited once per access before anything is signed.
func TestGrantStorageObjects_AuditAndOrder(t *testing.T) {
	f, st, sig := storageFixture(t)
	mem := "snap/" + depA + "/captures/" + captureC + "/v2/mem"
	vmstate := "snap/" + depA + "/captures/" + captureC + "/v2/vmstate"
	resp, code := grant(f, get("base/base-amd64.ext4"), get(mem), get(vmstate), create(mem+".age"))
	if code != codes.OK {
		t.Fatalf("code = %v", code)
	}
	for i, want := range []string{"base/base-amd64.ext4", mem, vmstate, mem + ".age"} {
		if resp.GetGrants()[i].GetKey() != want || resp.GetGrants()[i].GetUrl() != "https://store.test/"+want {
			t.Errorf("grant %d = %+v, want %s", i, resp.GetGrants()[i], want)
		}
	}
	if got := resp.GetGrants()[0].GetExpiresAt(); got != f.now.Add(storagegrant.TTL).UTC().Format(time.RFC3339) {
		t.Errorf("expires_at = %s", got)
	}
	want := []secretd.StorageGrantRecord{
		{NodeID: "n-owner", Class: "deployment", SubjectID: depA, AppID: "app-a", Access: "read", Objects: 2},
		{NodeID: "n-owner", Class: "deployment", SubjectID: depA, AppID: "app-a", Access: "write", Objects: 1},
	}
	if len(st.records) != len(want) {
		t.Fatalf("records = %+v, want %+v", st.records, want)
	}
	for i := range want {
		if st.records[i] != want[i] {
			t.Errorf("record %d = %+v, want %+v", i, st.records[i], want[i])
		}
	}
	if len(sig.signed) != 4 {
		t.Fatalf("signed %d", len(sig.signed))
	}

	st.failRecord = true
	sig.signed = nil
	if _, code := grant(f, get(mem)); code != codes.Unavailable || len(sig.signed) != 0 {
		t.Fatalf("unrecordable grant: code %v, signed %d; want Unavailable and nothing signed", code, len(sig.signed))
	}
}

// Platform reads stay out of the audit; platform creates and conditioned
// replaces are recorded under the nil subject (ADR-20261117190000
// amendment 1).
func TestGrantStorageObjects_PlatformWritesAudited(t *testing.T) {
	f, st, _ := storageFixture(t)
	if _, code := grant(f, get("base/base-amd64.ext4"), create("base/new-amd64.ext4"), create("scans/new-amd64.ext4.scan.json"),
		&secretdpb.StorageObjectRequest{Key: "base/base-amd64.ext4", Op: "replace", Etag: `"e"`}); code != codes.OK {
		t.Fatalf("code = %v", code)
	}
	want := secretd.StorageGrantRecord{NodeID: "n-owner", Class: "platform", SubjectID: "00000000-0000-0000-0000-000000000000", Access: "write", Objects: 3}
	if len(st.records) != 1 || st.records[0] != want {
		t.Fatalf("records = %+v, want [%+v]", st.records, want)
	}
}

func TestGrantStorageObjects_OffAndBounds(t *testing.T) {
	f := newFixture(t, 0)
	if _, err := f.server.GrantStorageObjects(f.ctx, &secretdpb.GrantStorageObjectsRequest{Objects: []*secretdpb.StorageObjectRequest{get("base/x")}}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("without storage grants: %v", err)
	}
	f, _, _ = storageFixture(t)
	if _, code := grant(f); code != codes.InvalidArgument {
		t.Fatalf("empty: %v", code)
	}
	many := make([]*secretdpb.StorageObjectRequest, storagegrant.MaxKeysPerRequest+1)
	for i := range many {
		many[i] = get("base/base-amd64.ext4")
	}
	if _, code := grant(f, many...); code != codes.InvalidArgument {
		t.Fatalf("too many: %v", code)
	}
}
