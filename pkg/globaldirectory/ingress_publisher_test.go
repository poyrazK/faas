package globaldirectory

// adr: 786
import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globalingress"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

type ingressFixture struct {
	quotaFixture
	publisher IngressPublisher
	exported  []byte
	server    *httptest.Server
	consents  []runtimequota.Consent
	change    func(runtimequota.ServingProbe)
	status    func(*runtimequota.ServingStatus)
}

// Regional ready responses are synthetic protocol fixtures, never native or
// billing qualification. The production receiver test separately proves frozen.
func seedIngressFixture(t *testing.T, f quotaFixture) *ingressFixture {
	t.Helper()
	x := &ingressFixture{quotaFixture: f}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := runtimequota.NewSigner(uuid.NewString(), key)
	if err != nil {
		t.Fatal(err)
	}
	trust, err := runtimequota.NewTrust([]runtimequota.Anchor{{IssuerID: signer.IssuerID(), Role: runtimequota.Directory, Public: pub}})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.(RuntimeQuotaIssuerStore).BindRuntimeQuotaIssuer(t.Context(), signer.IssuerID()); err != nil {
		t.Fatal(err)
	}
	x.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/ingress-status" || r.Header.Get("Authorization") != "" {
			t.Error("incorrect status transport")
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		e, err := runtimequota.DecodeServingProbe(raw)
		if err != nil {
			t.Error(err)
			return
		}
		p, err := trust.VerifyServingProbe(e)
		if err != nil {
			t.Error(err)
			return
		}
		if x.change != nil {
			x.change(p)
		}
		status := runtimequota.ServingStatus{Probe: p, ObservedAtMicros: time.Now().UnixMicro(), ExpiresAtMicros: p.ExpiresAtMicros, Healthy: true, Eligible: true, Code: "ready"}
		if x.status != nil {
			x.status(&status)
		}
		body, _ := json.Marshal(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(x.server.Close)
	cells, err := f.store.ListCells(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, cell := range cells {
		cell.APIBase = x.server.URL
		cell.AppsDomain = cell.Region + ".apps.example.com"
		cell, err = f.store.PutCell(t.Context(), cell)
		if err != nil {
			t.Fatal(err)
		}
		targets, err := f.store.ListTargets(t.Context(), f.owner.ID, f.request.Slug)
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range targets {
			if target.Region == cell.Region {
				b := target.Binding
				b.CellGeneration = cell.Generation
				if _, err = f.store.AttachTarget(t.Context(), f.owner.ID, f.request.Slug, b); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	a, err := f.quota.ReserveRuntimeQuota(t.Context(), f.request)
	if err != nil {
		t.Fatal(err)
	}
	for _, tgt := range a.Targets {
		tgt, err = f.quota.IssueRuntimeQuotaTarget(t.Context(), f.owner.ID, f.request.Slug, a.AllocationID, tgt.Region)
		if err != nil {
			t.Fatal(err)
		}
		grant, err := quotaWireGrant(signer.IssuerID(), a, tgt)
		if err != nil {
			t.Fatal(err)
		}
		c := runtimequota.Consent{ID: uuid.NewString(), IssuerID: grant.IssuerID, GlobalAppID: grant.GlobalAppID, OwnerID: grant.OwnerID, Binding: grant.Binding, ExpiresAtMicros: time.Now().Add(time.Hour).UnixMicro()}
		j := f.store.(RuntimeConsentStore)
		if _, err = j.PrepareRuntimeConsent(t.Context(), f.owner.ID, f.request.Slug, c); err != nil {
			t.Fatal(err)
		}
		if _, err = j.ObserveRuntimeConsent(t.Context(), f.owner.ID, f.request.Slug, c.ID, state.RegionalRuntimeConsent{Consent: c, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}); err != nil {
			t.Fatal(err)
		}
		x.consents = append(x.consents, c)
	}
	x.publisher = IngressPublisher{Store: f.store.(IngressStore), Signer: signer, Trust: trust, Verifier: Verifier{Client: x.server.Client()}, Export: func(raw []byte) error { x.exported = append([]byte(nil), raw...); return nil }}
	return x
}

func decodePublished(t *testing.T, x *ingressFixture) runtimequota.IngressSnapshot {
	t.Helper()
	e, err := runtimequota.DecodeIngressSnapshot(x.exported)
	if err != nil {
		t.Fatal(err)
	}
	s, err := x.publisher.Trust.VerifyIngressSnapshot(e, x.publisher.Signer.IssuerID())
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func exerciseIngressPublisher(t *testing.T, f quotaFixture) {
	t.Helper()
	x := seedIngressFixture(t, f)
	if err := x.publisher.Once(t.Context()); err != nil {
		t.Fatal(err)
	}
	s := decodePublished(t, x)
	if s.Generation != 1 || s.RoutingPolicy != AllSelectedRegions || len(s.Routes) != 1 || len(s.Routes[0].Targets) != 2 {
		t.Fatal("whole selected catalog", s)
	}
	for _, target := range s.Routes[0].Targets {
		if !target.Healthy || !target.Eligible {
			t.Fatal("ready target omitted", target)
		}
	}
	// Feed the actual publisher envelope into the consumer: all selected regions
	// rotate while a denied region is removed from the next full projection.
	var origins []string
	g, err := globalingress.New("global.example.com", s.DirectoryID, x.publisher.Trust, http.NotFoundHandler(), ingressRoundTrip(func(r *http.Request) (*http.Response, error) {
		origins = append(origins, r.URL.Host)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(&emptyReader{}), Request: r}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err = g.Refresh(x.exported); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		r := httptest.NewRequest("GET", "https://"+s.Routes[0].GlobalAppID+".global.example.com/hello", nil)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal("consumer", w.Code)
		}
	}
	if len(origins) != 4 || origins[0] == origins[1] || origins[0] != origins[2] {
		t.Fatal("all selected regions", origins)
	}
	x.status = func(s *runtimequota.ServingStatus) {
		if s.Probe.Grant.Binding.Region == "eu-hel" {
			s.Eligible = false
			s.Code = "admission_frozen"
		}
	}
	restarted := x.publisher
	if err = restarted.Once(t.Context()); err != nil {
		t.Fatal(err)
	}
	s = decodePublished(t, x)
	if s.Generation != 2 {
		t.Fatal("restart lost watermark", s.Generation)
	}
	if err = g.Refresh(x.exported); err != nil {
		t.Fatal(err)
	}
	origins = nil
	for range 2 {
		r := httptest.NewRequest("GET", "https://"+s.Routes[0].GlobalAppID+".global.example.com/hello", nil)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	if len(origins) != 2 || origins[0] != origins[1] {
		t.Fatal("frozen target routed", origins)
	}
	// A sink failure follows the durable commit; recovery exports the exact
	// retained envelope rather than allocating the generation a second time.
	x.publisher.Export = func([]byte) error { return errors.New("sink unavailable") }
	if err = x.publisher.Once(t.Context()); err == nil {
		t.Fatal("sink failure hidden")
	}
	if err = x.publisher.Store.ExportIngress(t.Context(), func(raw []byte) error { x.exported = raw; return nil }); err != nil {
		t.Fatal(err)
	}
	if s = decodePublished(t, x); s.Generation != 3 {
		t.Fatal("commit lost on export failure", s.Generation)
	}
}

type ingressRoundTrip func(*http.Request) (*http.Response, error)

func (f ingressRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type emptyReader struct{}

func (*emptyReader) Read([]byte) (int, error) { return 0, io.EOF }

func TestIngressPublisherMemory(t *testing.T) {
	exerciseIngressPublisher(t, memoryQuotaFixture(t, false, 2))
}
func exerciseIngressIntentFence(t *testing.T, f quotaFixture) {
	t.Helper()
	x := seedIngressFixture(t, f)
	if err := x.publisher.Once(t.Context()); err != nil {
		t.Fatal(err)
	}
	original := append([]byte(nil), x.exported...)
	var once sync.Once
	x.change = func(p runtimequota.ServingProbe) {
		once.Do(func() {
			if err := f.store.DetachTarget(t.Context(), f.owner.ID, f.request.Slug, p.Grant.Binding.Region); err != nil {
				t.Error(err)
			}
		})
	}
	if err := x.publisher.Once(t.Context()); !errors.Is(err, ErrIngressChanged) {
		t.Fatal("stale observation committed", err)
	}
	if string(x.exported) != string(original) {
		t.Fatal("failed fence changed file")
	}
	x.change = nil
	if err := x.publisher.Once(t.Context()); err != nil {
		t.Fatal(err)
	}
	s := decodePublished(t, x)
	if s.Generation != 2 || len(s.Routes[0].Targets) != 1 {
		t.Fatal("detach retained stale route", s)
	}
}
func TestIngressPublisherMemoryIntentFence(t *testing.T) {
	exerciseIngressIntentFence(t, memoryQuotaFixture(t, false, 2))
}

func TestIngressPublisherUncertainStatusFailsClosed(t *testing.T) {
	for _, kind := range []string{"nonce", "origin", "expiry", "issuer", "unknown", "frozen"} {
		t.Run(kind, func(t *testing.T) {
			x := seedIngressFixture(t, memoryQuotaFixture(t, false, 1))
			x.status = func(s *runtimequota.ServingStatus) {
				switch kind {
				case "nonce":
					s.Probe.Nonce = uuid.NewString()
				case "origin":
					s.Probe.Origin = "https://other.example.com"
				case "expiry":
					s.ExpiresAtMicros = s.ObservedAtMicros
				case "issuer":
					s.Probe.Grant.IssuerID = uuid.NewString()
				case "unknown":
					s.Code = "operator_ready"
				case "frozen":
					s.Code = "admission_frozen"
					s.Eligible = false
				}
			}
			if err := x.publisher.Once(t.Context()); err != nil {
				t.Fatal(err)
			}
			s := decodePublished(t, x)
			if len(s.Routes) != 1 || len(s.Routes[0].Targets) != 1 || s.Routes[0].Targets[0].Eligible {
				t.Fatal("uncertain status became route", s)
			}
		})
	}
}

func TestIngressPublisherRedirectDenied(t *testing.T) {
	x := seedIngressFixture(t, memoryQuotaFixture(t, false, 1))
	x.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://replacement.example.com", http.StatusTemporaryRedirect)
	})
	if err := x.publisher.Once(t.Context()); err != nil {
		t.Fatal(err)
	}
	if s := decodePublished(t, x); s.Routes[0].Targets[0].Eligible {
		t.Fatal("redirect authorized traffic")
	}
}

func TestIngressCatalogOwnerConsentQuotaGates(t *testing.T) {
	for _, kind := range []string{"plan", "hold", "consent", "retirement", "registry", "unissued"} {
		t.Run(kind, func(t *testing.T) {
			x := seedIngressFixture(t, memoryQuotaFixture(t, false, 1))
			f := x.quotaFixture
			switch kind {
			case "plan":
				if err := f.authority.UpdateAccountPlan(t.Context(), f.actor.ID, api.PlanHobby); err != nil {
					t.Fatal(err)
				}
			case "hold":
				if err := f.authority.UpdateAccountStatus(t.Context(), f.actor.ID, state.AccountSuspended); err != nil {
					t.Fatal(err)
				}
			case "consent":
				if _, err := f.store.(RuntimeConsentStore).BeginRevokeRuntimeConsent(t.Context(), f.owner.ID, f.request.Slug, x.consents[0].ID); err != nil {
					t.Fatal(err)
				}
			case "retirement":
				if _, err := f.quota.RetireRuntimeQuota(t.Context(), f.owner.ID, f.actor.ID, f.request.Slug, f.request.AllocationID); err != nil {
					t.Fatal(err)
				}
			case "unissued":
				req := f.request
				req.AllocationID = uuid.NewString()
				if _, err := f.quota.ReserveRuntimeQuota(t.Context(), req); err != nil {
					t.Fatal(err)
				}
			case "registry":
				cell, err := f.store.CellByRegion(t.Context(), "eu-fsn")
				if err != nil {
					t.Fatal(err)
				}
				cell.Enabled = false
				if _, err = f.store.PutCell(t.Context(), cell); err != nil {
					t.Fatal(err)
				}
			}
			if err := x.publisher.Once(t.Context()); err != nil {
				t.Fatal(err)
			}
			s := decodePublished(t, x)
			if len(s.Routes) != 1 || len(s.Routes[0].Targets) != 0 {
				t.Fatal("ineligible intent selected", s)
			}
		})
	}
}

func exerciseIngressConcurrentPublishers(t *testing.T, f quotaFixture) {
	t.Helper()
	x := seedIngressFixture(t, f)
	path := t.TempDir() + "/routes.json"
	p := x.publisher
	p.Export = func(raw []byte) error { return globalingress.WriteManifest(path, raw) }
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for range 6 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- p.Once(t.Context()) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	raw, err := globalingress.ReadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	x.exported = raw
	if s := decodePublished(t, x); s.Generation != 6 {
		t.Fatal("older publisher overwrote latest", s.Generation)
	}
	if err = p.Store.ExportIngress(t.Context(), func(latest []byte) error {
		if string(latest) != string(raw) {
			t.Error("file differs from durable publication")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
func TestIngressConcurrentPublishersMemory(t *testing.T) {
	exerciseIngressConcurrentPublishers(t, memoryQuotaFixture(t, false, 2))
}

func TestIngressCatalogComparisonAndCanceledCommit(t *testing.T) {
	c := IngressCatalog{{App: App{CreatedAt: time.Date(-1, 1, 1, 0, 0, 0, 0, time.UTC)}}}
	if sameIngressCatalog(c, c) {
		t.Fatal("failed canonical serialization accepted")
	}
	x := seedIngressFixture(t, memoryQuotaFixture(t, false, 1))
	catalog, err := x.publisher.Store.IngressCatalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	err = x.publisher.Store.CommitIngress(ctx, catalog, x.publisher.Signer.IssuerID(), func(g int64) (runtimequota.Envelope, error) {
		cancel()
		return x.publisher.Signer.SignIngressSnapshot(ingressSnapshot(catalog, x.publisher.Signer.IssuerID(), g, nil, time.Now()))
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("canceled commit accepted", err)
	}
	if err = x.publisher.Once(t.Context()); err != nil {
		t.Fatal(err)
	}
	if s := decodePublished(t, x); s.Generation != 1 {
		t.Fatal("canceled commit consumed generation", s.Generation)
	}
}
