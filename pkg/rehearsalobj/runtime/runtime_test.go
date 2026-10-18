package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/rehearsalobj"
	"github.com/onebox-faas/faas/pkg/state"
)

type objectFixture struct {
	base                state.Store
	pool                *pgxpool.Pool
	catalog             rehearsalobj.Catalog
	service             *Service
	handler             *Handler
	app                 state.App
	account             state.Account
	run, access, secret string
	req                 rehearsalobj.CreateRequest
	receipt             rehearsalobj.Receipt
	stopCollector       context.CancelFunc
}

func newObjectFixture(t *testing.T, backend string) *objectFixture {
	t.Helper()
	f := &objectFixture{run: strings.ReplaceAll(uuid.NewString(), "-", ""), access: "GRTEST" + strings.ReplaceAll(uuid.NewString(), "-", ""), secret: "test-secret-" + uuid.NewString()}
	if backend == "postgres" {
		f.pool = pgtest.OpenMigrated(t)
		f.base = state.NewPgStore(f.pool)
	} else {
		f.base = state.NewMemStore()
	}
	f.catalog = f.base.(rehearsalobj.Catalog)
	f.service = NewService(f.catalog)
	collector := NewCollector(f.catalog)
	collectorCtx, stopCollector := context.WithCancel(t.Context())
	f.stopCollector = stopCollector
	t.Cleanup(stopCollector)
	go collector.Run(collectorCtx)
	var e error
	f.account, e = f.base.CreateAccount(t.Context(), uuid.NewString()+"@objects.test", api.PlanPro)
	if e != nil {
		t.Fatal(e)
	}
	expires := time.Now().Add(time.Hour)
	f.app, e = f.base.CreateApp(t.Context(), state.App{ID: uuid.NewString(), AccountID: f.account.ID, Slug: "objects-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, PreviewOfSlug: "source", PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
	if e != nil {
		t.Fatal(e)
	}
	plan := api.ScenarioSimulationPlan{Version: 1, Fixtures: []api.ScenarioSimulationFixture{{Workload: "primary", Name: "notify", Integration: "email", Adapter: "resend", Method: "POST", Path: "/emails", Responses: []api.ScenarioSimulationResponse{{Status: 200}}}}}
	body, _, e := api.NormalizeScenarioSimulationPlan(plan, []string{"primary"})
	if e != nil {
		t.Fatal(e)
	}
	if e = f.base.RegisterScenarioTestMembers(t.Context(), f.account.ID, f.run, []state.ScenarioTestMember{{AppID: f.app.ID, Workload: "primary", WebhookMode: "capture", NetworkMode: "contained", SimulationJSON: string(body)}}); e != nil {
		t.Fatal(e)
	}
	objects := []rehearsalobj.Data{{Key: "customers/other.txt", Body: []byte("private original"), ContentType: "text/plain", Metadata: map[string]string{"owner": "other"}}}
	initial, _ := rehearsalobj.Snapshot(objects)
	f.req = rehearsalobj.CreateRequest{AppSlug: f.app.Slug, Spec: rehearsalobj.Spec{Version: 1, Bucket: "imports", FixtureSHA256: rehearsalobj.Hash(initial)}, Objects: objects}
	f.receipt, e = f.service.Create(t.Context(), f.account.ID, f.run, f.app.ID, f.access, []byte("sealed-fixture-only"), f.req)
	if e != nil {
		t.Fatal(e)
	}
	f.handler = &Handler{Catalog: f.catalog, Collector: collector, Resolve: func(context.Context, string) (string, string, error) { return f.app.ID, "deployment", nil }, OpenSecret: func([]byte) (string, error) { return f.secret, nil }}
	t.Cleanup(func() { _ = f.service.Delete(context.Background(), f.account.ID, f.run) })
	return f
}
func (f *objectFixture) request(t *testing.T, method, target string, body []byte, headers map[string]string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, "http://primary.svc.gregale:10084"+target, bytes.NewReader(body))
	r.Header.Set("X-Amz-Content-Sha256", rehearsalobj.HashBytes(body))
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	if e := awsv4.NewSigner(func(o *awsv4.SignerOptions) { o.DisableURIPathEscaping = true }).SignHTTP(t.Context(), aws.Credentials{AccessKeyID: f.access, SecretAccessKey: f.secret}, r, rehearsalobj.HashBytes(body), "s3", rehearsalobj.Region, time.Now()); e != nil {
		t.Fatal(e)
	}
	return r
}
func (f *objectFixture) do(t *testing.T, method, target string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, f.request(t, method, target, body, headers))
	return w
}
func (f *objectFixture) seal(t *testing.T) rehearsalobj.Evidence {
	t.Helper()
	e, err := f.service.Seal(t.Context(), f.account.ID, f.run, "deployment")
	if err != nil || e.Validate() != nil {
		t.Fatalf("seal: %v; evidence=%+v", err, e)
	}
	r, err := f.catalog.GetScenarioObjects(t.Context(), f.account.ID, f.run)
	if err != nil || len(r.Current) != 0 || r.Status != "complete" {
		t.Fatal("complete evidence retained object bytes")
	}
	return e
}

func TestObjectRehearsalDetectsDuplicateUploadsAndCrossCustomerOverwrite(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			old := newObjectFixture(t, backend)
			candidate := newObjectFixture(t, backend)
			for _, f := range []*objectFixture{old, candidate} {
				if w := f.do(t, "PUT", "/imports/exports/result.txt", []byte("same export"), nil); w.Code != 200 {
					t.Fatalf("put: %d %s", w.Code, w.Body.String())
				}
			}
			baseline := old.seal(t)
			if w := candidate.do(t, "PUT", "/imports/exports/result.txt", []byte("same export"), nil); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			next := candidate.seal(t)
			if rehearsalobj.Hash(baseline.After) != rehearsalobj.Hash(next.After) {
				t.Fatal("duplicate changed final state")
			}
			c := rehearsalobj.Compare(&baseline, &next)
			if c.Status != "failed" || c.BeforeMutations != 1 || c.AfterMutations != 2 {
				t.Fatalf("duplicate passed: %+v", c)
			}
			overwrite := newObjectFixture(t, backend)
			if w := overwrite.do(t, "PUT", "/imports/customers/other.txt", []byte("wrong customer"), nil); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			e := overwrite.seal(t)
			if c := rehearsalobj.Compare(&baseline, &e); c.Status != "failed" {
				t.Fatal("wrong-customer overwrite passed")
			}
			encoded, _ := json.Marshal(next)
			if bytes.Contains(encoded, []byte("same export")) || bytes.Contains(encoded, []byte(candidate.secret)) {
				t.Fatal("evidence contains content or credentials")
			}
		})
	}
}
func TestObjectRehearsalS3SDKReadsWritesListsAndDeletes(t *testing.T) {
	f := newObjectFixture(t, "memory")
	server := httptest.NewServer(f.handler)
	defer server.Close()
	client := s3.NewFromConfig(aws.Config{Region: rehearsalobj.Region, Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: f.access, SecretAccessKey: f.secret}, nil
	}), HTTPClient: server.Client(), RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired, ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired}, func(o *s3.Options) { o.BaseEndpoint = aws.String(server.URL); o.UsePathStyle = true })
	key := "exports/é + final.txt"
	bucket := "imports"
	body := []byte{0, 1, 2, 255, 10}
	if _, e := client.PutObject(t.Context(), &s3.PutObjectInput{Bucket: &bucket, Key: &key, Body: bytes.NewReader(body), ContentType: aws.String("application/octet-stream"), Metadata: map[string]string{"owner": "first"}}); e != nil {
		t.Fatal(e)
	}
	get, e := client.GetObject(t.Context(), &s3.GetObjectInput{Bucket: &bucket, Key: &key})
	if e != nil {
		t.Fatal(e)
	}
	data, e := io.ReadAll(get.Body)
	_ = get.Body.Close()
	if e != nil || !bytes.Equal(data, body) || get.Metadata["owner"] != "first" {
		t.Fatal("read changed object")
	}
	if head, e := client.HeadObject(t.Context(), &s3.HeadObjectInput{Bucket: &bucket, Key: &key}); e != nil || aws.ToInt64(head.ContentLength) != int64(len(body)) {
		t.Fatalf("head: %v", e)
	}
	if _, e := client.HeadBucket(t.Context(), &s3.HeadBucketInput{Bucket: &bucket}); e != nil {
		t.Fatal(e)
	}
	list, e := client.ListObjectsV2(t.Context(), &s3.ListObjectsV2Input{Bucket: &bucket, Prefix: aws.String("exports/")})
	if e != nil || len(list.Contents) != 1 || aws.ToString(list.Contents[0].Key) != key {
		t.Fatalf("list: %+v %v", list, e)
	}
	if _, e := client.DeleteObject(t.Context(), &s3.DeleteObjectInput{Bucket: &bucket, Key: &key}); e != nil {
		t.Fatal(e)
	}
	evidence := f.seal(t)
	if len(evidence.Mutations) != 2 || len(evidence.After) != 1 {
		t.Fatal("SDK effects not independently collected")
	}
}
func TestObjectRehearsalUnsupportedOrInterruptedRequestsCannotPass(t *testing.T) {
	for _, name := range []string{"multipart", "cross bucket", "wrong signature", "hash mismatch", "range", "condition", "copy", "oversize", "bad checksum", "truncated", "metadata bound", "invalid query"} {
		t.Run(name, func(t *testing.T) {
			f := newObjectFixture(t, "memory")
			r := f.request(t, "PUT", "/imports/export.txt", []byte("data"), nil)
			switch name {
			case "multipart":
				r = f.request(t, "POST", "/imports/export.txt?uploads=", nil, nil)
			case "cross bucket":
				r = f.request(t, "PUT", "/production/export.txt", []byte("data"), nil)
			case "wrong signature":
				r.URL.Path = "/imports/tampered.txt"
			case "hash mismatch":
				r.Body = io.NopCloser(strings.NewReader("evil"))
			case "range":
				r = f.request(t, "GET", "/imports/customers/other.txt", nil, map[string]string{"Range": "bytes=0-2"})
			case "condition":
				r = f.request(t, "PUT", "/imports/export.txt", []byte("data"), map[string]string{"If-None-Match": "*"})
			case "copy":
				r = f.request(t, "PUT", "/imports/export.txt", nil, map[string]string{"X-Amz-Copy-Source": "/production/source"})
			case "oversize":
				r = f.request(t, "PUT", "/imports/export.txt", bytes.Repeat([]byte("x"), rehearsalobj.MaxObjectBytes+1), nil)
			case "bad checksum":
				r = f.request(t, "PUT", "/imports/export.txt", []byte("data"), map[string]string{"Content-MD5": "wrong"})
			case "truncated":
				r.Body = io.NopCloser(io.MultiReader(strings.NewReader("d"), errorReader{}))
			case "metadata bound":
				r = f.request(t, "PUT", "/imports/export.txt", nil, map[string]string{"X-Amz-Meta-Owner": strings.Repeat("x", 257)})
			case "invalid query":
				r = f.request(t, "GET", "/imports?list-type=2&unknown=1", nil, nil)
			}
			w := httptest.NewRecorder()
			f.handler.ServeHTTP(w, r)
			if w.Code < 400 {
				t.Fatalf("unsupported request succeeded: %d", w.Code)
			}
			if _, e := f.service.Seal(t.Context(), f.account.ID, f.run, "deployment"); e == nil {
				t.Fatal("unsupported or incomplete request produced evidence")
			}
			if e := f.service.Delete(t.Context(), f.account.ID, f.run); e != nil {
				t.Fatal(e)
			}
		})
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type blockedReader struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	body    *bytes.Reader
}

func (b *blockedReader) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.started); <-b.release })
	return b.body.Read(p)
}
func (b *blockedReader) Close() error { return nil }
func TestObjectRehearsalSealingFencesAdmissionAndJoinsUploads(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newObjectFixture(t, backend)
			body := []byte("first admitted, last completed")
			r := f.request(t, "PUT", "/imports/result", body, nil)
			blocked := &blockedReader{started: make(chan struct{}), release: make(chan struct{}), body: bytes.NewReader(body)}
			r.Body = blocked
			writeDone := make(chan int, 1)
			go func() { w := httptest.NewRecorder(); f.handler.ServeHTTP(w, r); writeDone <- w.Code }()
			<-blocked.started
			if w := f.do(t, "PUT", "/imports/result", []byte("second completes first"), nil); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			type sealResult struct {
				e   rehearsalobj.Evidence
				err error
			}
			sealed := make(chan sealResult, 1)
			go func() {
				e, err := f.service.Seal(t.Context(), f.account.ID, f.run, "deployment")
				sealed <- sealResult{e, err}
			}()
			deadline := time.NewTimer(2 * time.Second)
			defer deadline.Stop()
			for {
				resource, e := f.catalog.GetScenarioObjects(t.Context(), f.account.ID, f.run)
				if e != nil {
					t.Fatal(e)
				}
				if resource.Status == "sealing" {
					break
				}
				select {
				case <-deadline.C:
					t.Fatal("sealing did not fence admission")
				case <-time.After(time.Millisecond):
				}
			}
			if w := f.do(t, "PUT", "/imports/late", []byte("late"), nil); w.Code != 403 {
				t.Fatalf("late request admitted: %d", w.Code)
			}
			close(blocked.release)
			if code := <-writeDone; code != 200 {
				t.Fatalf("admitted write did not join: %d", code)
			}
			result := <-sealed
			if result.err != nil || result.e.Validate() != nil || len(result.e.Mutations) != 2 || result.e.Mutations[1].Object.SHA256 != rehearsalobj.HashBytes(body) {
				t.Fatalf("join: %+v %v", result.e, result.err)
			}
		})
	}
}
func TestObjectJournalRejectsForeignCallersAndImmutableIntent(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newObjectFixture(t, backend)
			if _, e := f.catalog.GetScenarioObjects(t.Context(), uuid.NewString(), f.run); !errors.Is(e, state.ErrNotFound) {
				t.Fatal("foreign account read resource")
			}
			if _, e := f.catalog.ScenarioObjectsForApp(t.Context(), uuid.NewString()); !errors.Is(e, state.ErrNotFound) {
				t.Fatal("unknown caller resolved resource")
			}
			if _, e := f.service.Create(t.Context(), f.account.ID, f.run, f.app.ID, f.access, []byte("sealed"), f.req); e == nil {
				t.Fatal("run reused namespace")
			}
			if _, e := f.catalog.UpdateScenarioObjects(t.Context(), f.account.ID, f.run, "", func(r *rehearsalobj.Resource) error { r.Spec.Bucket = "production"; return nil }); e == nil {
				t.Fatal("fixture intent changed")
			}
			resource, e := f.catalog.GetScenarioObjects(t.Context(), f.account.ID, f.run)
			if e != nil {
				t.Fatal(e)
			}
			resource.Current[0].Body[0] = 'X'
			retained, _ := f.catalog.GetScenarioObjects(t.Context(), f.account.ID, f.run)
			if retained.Current[0].Body[0] == 'X' {
				t.Fatal("resource aliases caller data")
			}
			if f.pool != nil {
				if _, e = f.pool.Exec(t.Context(), `DELETE FROM scenario_object_fixtures WHERE id=$1`, resource.ID); e == nil {
					t.Fatal("live namespace purged")
				}
				if _, e = f.pool.Exec(t.Context(), `UPDATE scenario_object_fixtures SET document=jsonb_set(document,'{Spec,Bucket}','"production"') WHERE id=$1`, resource.ID); e == nil {
					t.Fatal("SQL changed fixture intent")
				}
			}
			evidence := f.seal(t)
			again, e := f.service.Seal(t.Context(), f.account.ID, f.run, "deployment")
			if e != nil || rehearsalobj.Hash(evidence) != rehearsalobj.Hash(again) {
				t.Fatal("seal was not idempotent")
			}
			if _, e = f.catalog.UpdateScenarioObjects(t.Context(), f.account.ID, f.run, "", func(r *rehearsalobj.Resource) error { r.Status = "ready"; return nil }); e == nil {
				t.Fatal("terminal namespace reopened")
			}
			if f.pool != nil {
				if _, e = f.pool.Exec(t.Context(), `DELETE FROM scenario_object_fixtures WHERE id=$1`, resource.ID); e != nil {
					t.Fatalf("cleaned journal blocked retention: %v", e)
				}
			}
		})
	}
}

func TestObjectRehearsalLostRequestsAndWrongDeploymentsAreInconclusive(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		for _, fault := range []string{"pending", "different deployment", "canceled collector"} {
			t.Run(backend+"/"+fault, func(t *testing.T) {
				f := newObjectFixture(t, backend)
				ctx := t.Context()
				switch fault {
				case "pending":
					_, e := f.catalog.UpdateScenarioObjects(ctx, f.account.ID, f.run, f.app.ID, func(r *rehearsalobj.Resource) error {
						r.Attempts = append(r.Attempts, rehearsalobj.Attempt{ID: uuid.NewString(), DeploymentID: "deployment", Method: "PUT", Key: "unfinished", Pending: true})
						return nil
					})
					if e != nil {
						t.Fatal(e)
					}
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, 60*time.Millisecond)
					defer cancel()
				case "different deployment":
					f.handler.Resolve = func(context.Context, string) (string, string, error) { return f.app.ID, "other-deployment", nil }
					if w := f.do(t, "PUT", "/imports/result", []byte("other release"), nil); w.Code != 200 {
						t.Fatal(w.Body.String())
					}
				case "canceled collector":
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
				}
				if _, e := f.service.Seal(ctx, f.account.ID, f.run, "deployment"); e == nil {
					t.Fatal("incomplete or unbound evidence passed")
				}
				r, e := f.catalog.GetScenarioObjects(t.Context(), f.account.ID, f.run)
				if e != nil || r.Status != "deleted" || len(r.Current) != 0 || r.Evidence != nil {
					t.Fatalf("failed collection did not clean up: %+v %v", r, e)
				}
			})
		}
	}
}

type faultObjectCatalog struct {
	base                rehearsalobj.Catalog
	completion, cleanup bool
}

func (c *faultObjectCatalog) ReserveScenarioObjects(ctx context.Context, r rehearsalobj.Resource) (rehearsalobj.Resource, error) {
	return c.base.ReserveScenarioObjects(ctx, r)
}
func (c *faultObjectCatalog) GetScenarioObjects(ctx context.Context, account, run string) (rehearsalobj.Resource, error) {
	return c.base.GetScenarioObjects(ctx, account, run)
}
func (c *faultObjectCatalog) ScenarioObjectsForApp(ctx context.Context, app string) (rehearsalobj.Resource, error) {
	return c.base.ScenarioObjectsForApp(ctx, app)
}
func (c *faultObjectCatalog) ExpiredScenarioObjects(ctx context.Context) ([]rehearsalobj.Resource, error) {
	return c.base.ExpiredScenarioObjects(ctx)
}
func (c *faultObjectCatalog) UpdateScenarioObjects(ctx context.Context, account, run, app string, update func(*rehearsalobj.Resource) error) (rehearsalobj.Resource, error) {
	return c.base.UpdateScenarioObjects(ctx, account, run, app, func(r *rehearsalobj.Resource) error {
		if e := update(r); e != nil {
			return e
		}
		if c.completion && r.Status == "complete" || c.cleanup && r.Status == "deleted" {
			return errors.New("injected transaction failure")
		}
		return nil
	})
}
func TestObjectRehearsalCompletionAndCleanupFailuresNeverPublishEvidence(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newObjectFixture(t, backend)
			faults := &faultObjectCatalog{base: f.catalog, completion: true, cleanup: true}
			service := NewService(faults)
			if _, e := service.Seal(t.Context(), f.account.ID, f.run, "deployment"); e == nil {
				t.Fatal("failed transaction published evidence")
			}
			r, e := f.catalog.GetScenarioObjects(t.Context(), f.account.ID, f.run)
			if e != nil || r.Status != "sealing" || r.Evidence != nil || len(r.Current) == 0 {
				t.Fatal("failed cleanup was falsely acknowledged")
			}
			if e = service.Delete(t.Context(), f.account.ID, f.run); e == nil {
				t.Fatal("failed cleanup succeeded")
			}
			faults.cleanup = false
			if e = service.Delete(t.Context(), f.account.ID, f.run); e != nil {
				t.Fatal(e)
			}
			r, _ = f.catalog.GetScenarioObjects(t.Context(), f.account.ID, f.run)
			if r.Status != "deleted" || len(r.Current) != 0 {
				t.Fatal("cleanup retry did not remove bodies")
			}
		})
	}
}

func (c *faultObjectCatalog) ActiveScenarioObjects(ctx context.Context, after string) ([]rehearsalobj.Resource, error) {
	return c.base.ActiveScenarioObjects(ctx, after)
}

func TestObjectCollectorReplacementCannotAdoptAnEmptyCaptureWindow(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newObjectFixture(t, backend)
			f.stopCollector()
			replacement := NewCollector(f.catalog)
			if _, e := replacement.Step(t.Context(), ""); e != nil {
				t.Fatal(e)
			}
			r, e := f.catalog.GetScenarioObjects(t.Context(), f.account.ID, f.run)
			if e != nil || r.CollectorID == replacement.ID || r.CollectorClosed {
				t.Fatal("replacement adopted the old capture window")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
			defer cancel()
			if _, e = f.service.Seal(ctx, f.account.ID, f.run, "deployment"); e == nil {
				t.Fatal("collector loss passed an empty inventory")
			}
			r, _ = f.catalog.GetScenarioObjects(t.Context(), f.account.ID, f.run)
			if r.Status != "deleted" || len(r.Current) != 0 {
				t.Fatal("lost owner prevented cleanup")
			}
		})
	}
}
func TestObjectCollectorFailureCannotCloseAPassingWindow(t *testing.T) {
	f := newObjectFixture(t, "memory")
	f.handler.Collector.Fail()
	if _, e := f.service.Seal(t.Context(), f.account.ID, f.run, "deployment"); e == nil {
		t.Fatal("lost capture passed without request rows")
	}
}

func TestObjectCollectorDetectsUnjournaledContentAndProtectsReceipts(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newObjectFixture(t, backend)
			if w := f.do(t, "PUT", "/imports/result", []byte("recorded"), nil); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			if _, e := f.catalog.UpdateScenarioObjects(t.Context(), f.account.ID, f.run, "", func(r *rehearsalobj.Resource) error { r.Attempts[0].Mutation.Status = 201; return nil }); e == nil {
				t.Fatal("completed attempt could be changed")
			}
			if f.pool != nil {
				if _, e := f.pool.Exec(t.Context(), `UPDATE scenario_object_fixtures SET document=jsonb_set(document,'{Attempts,0,Mutation,status}','201') WHERE id=$1`, f.receipt.ResourceID); e == nil {
					t.Fatal("SQL changed a completed attempt")
				}
			}
			// Model an independent storage corruption or missed operation. Final
			// collection must derive its digest from bytes rather than receipts.
			if _, e := f.catalog.UpdateScenarioObjects(t.Context(), f.account.ID, f.run, "", func(r *rehearsalobj.Resource) error { r.Current[0].Body = []byte("unjournaled"); return nil }); e != nil {
				t.Fatal(e)
			}
			if _, e := f.service.Seal(t.Context(), f.account.ID, f.run, "deployment"); e == nil {
				t.Fatal("unjournaled content produced complete evidence")
			}
		})
	}
}
