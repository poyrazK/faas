package meter_test

// adr: 802
import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

type usageDeliveryFixture struct {
	global, regional         *state.MemStore
	account, regionalAccount state.Account
	directory, cell          runtimequota.Signer
	dirs, cells              runtimequota.Trust
	usage                    runtimequota.RuntimeUsage
	now                      time.Time
	receiver                 meter.GlobalRuntimeUsageReceiver
}

func usageDeliverySigner(t *testing.T, issuer string, role string) (runtimequota.Signer, runtimequota.Trust) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := runtimequota.NewSigner(issuer, key)
	if err != nil {
		t.Fatal(err)
	}
	anchor, err := signer.PublicAnchor(role)
	if err != nil {
		t.Fatal(err)
	}
	trust, err := runtimequota.NewTrust([]runtimequota.Anchor{anchor})
	if err != nil {
		t.Fatal(err)
	}
	return signer, trust
}

func newUsageDeliveryFixture(t *testing.T) usageDeliveryFixture {
	t.Helper()
	ctx := t.Context()
	f := usageDeliveryFixture{global: state.NewMemStore(), regional: state.NewMemStore()}
	f.account = makeBillableAccount(t, ctx, f.global, api.PlanPro)
	if err := f.global.MarkAccountEmailVerified(ctx, f.account.ID); err != nil {
		t.Fatal(err)
	}
	var err error
	f.regionalAccount, err = f.regional.CreateAccount(ctx, uuid.NewString()+"@usage.example", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	regionalApp, err := f.regional.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: f.regionalAccount.ID, Slug: "regional-usage", Type: state.AppTypeApp, Runtime: "node22", RAMMB: 128, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	directory := globaldirectory.NewMemoryStoreWithOrgAuthority(f.global)
	f.directory, f.dirs = usageDeliverySigner(t, uuid.NewString(), runtimequota.Directory)
	if err := directory.BindRuntimeQuotaIssuer(ctx, f.directory.IssuerID()); err != nil {
		t.Fatal(err)
	}
	app, err := directory.CreateApp(ctx, f.account.ID, "usage-delivery", 5)
	if err != nil {
		t.Fatal(err)
	}
	cell, err := directory.PutCell(ctx, globaldirectory.Cell{ID: uuid.NewString(), Region: "eu-fsn", APIBase: "https://regional.example.com", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	binding := globaldirectory.Binding{CellID: cell.ID, Region: cell.Region, CellGeneration: cell.Generation, RegionalAccountID: uuid.MustParse(regionalApp.AccountID).String(), RegionalAppID: regionalApp.ID, RegionalAppSlug: regionalApp.Slug}
	if _, err := directory.AttachTarget(ctx, f.account.ID, app.Slug, binding); err != nil {
		t.Fatal(err)
	}
	allocation := uuid.NewString()
	if _, err := directory.ReserveRuntimeQuota(ctx, globaldirectory.RuntimeQuotaRequest{OwnerID: f.account.ID, ActorID: f.account.ID, Slug: app.Slug, AllocationID: allocation, ExpectedPlan: f.account.Plan, Shares: []globaldirectory.RuntimeQuotaShare{{Region: cell.Region, Concurrency: 1}}}); err != nil {
		t.Fatal(err)
	}
	authority := globaldirectory.RuntimeQuotaAuthority{Store: directory, Signer: f.directory}
	envelope, err := authority.Issue(ctx, f.account.ID, app.Slug, allocation, cell.Region)
	if err != nil {
		t.Fatal(err)
	}
	runtimeBinding := runtimequota.Binding{CellID: cell.ID, Region: cell.Region, CellGeneration: cell.Generation, RegionalAccountID: binding.RegionalAccountID, RegionalAppID: regionalApp.ID, RegionalAppSlug: regionalApp.Slug, APIBase: cell.APIBase}
	grant, err := f.dirs.VerifyGrant(envelope, runtimeBinding)
	if err != nil {
		t.Fatal(err)
	}
	regionalAuthority := state.RegionalRuntimeQuotaAuthority{Store: f.regional, Directories: f.dirs, Binding: runtimeBinding}
	if _, err := regionalAuthority.AcceptGrant(ctx, envelope); err != nil {
		t.Fatal(err)
	}
	f.cell, f.cells = usageDeliverySigner(t, cell.ID, runtimequota.Cell)
	minute := time.UnixMicro(grant.IssuedAtMicros).UTC().Truncate(time.Minute)
	digest := sha256.Sum256([]byte("complete original cell measurements"))
	f.usage = runtimequota.RuntimeUsage{Grant: grant, MinuteMicros: minute.UnixMicro(), MBSeconds: 520 * 17, SourceCount: 1, SourceDigest: hex.EncodeToString(digest[:]), ObservedAtMicros: minute.Add(time.Minute).UnixMicro()}
	f.now = minute.Add(3 * time.Minute)
	f.receiver = meter.GlobalRuntimeUsageReceiver{Store: f.global, Cells: f.cells, Directories: f.dirs, Signer: f.directory, Now: func() time.Time { return f.now }}
	return f
}

func (f usageDeliveryFixture) queue(t *testing.T, u runtimequota.RuntimeUsage) state.RegionalRuntimeUsageOutboxItem {
	t.Helper()
	e, err := f.cell.SignRuntimeUsage(u)
	if err != nil {
		t.Fatal(err)
	}
	row, err := state.QueueRegionalRuntimeUsage(t.Context(), f.regional, f.cells, e, f.now)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func (f usageDeliveryFixture) sender(t *testing.T, handler http.Handler) (*meter.RuntimeUsageHTTPSender, *httptest.Server) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	sender, err := meter.NewRuntimeUsageHTTPSender(map[string]string{f.directory.IssuerID(): server.URL}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return sender, server
}

func (f usageDeliveryFixture) delivery(sender meter.RuntimeUsageSender) meter.RuntimeUsageDelivery {
	return meter.RuntimeUsageDelivery{Store: f.regional, Sender: sender, Directories: f.dirs, Now: func() time.Time { return f.now }}
}

type usageSenderFunc func(context.Context, state.RegionalRuntimeUsageOutboxItem) (runtimequota.Envelope, error)

func (fn usageSenderFunc) Send(ctx context.Context, row state.RegionalRuntimeUsageOutboxItem) (runtimequota.Envelope, error) {
	return fn(ctx, row)
}

type usageRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn usageRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func TestRegionalRuntimeUsageDeliveryLostReplyAndConcurrentRetry(t *testing.T) {
	f := newUsageDeliveryFixture(t)
	item := f.queue(t, f.usage)
	var requests atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != meter.RuntimeUsageImportPath || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Authorization") != "" {
			t.Error("incorrect import request")
		}
		f.receiver.ServeHTTP(w, r)
	})
	_, server := f.sender(t, handler)
	client := server.Client()
	transport := client.Transport
	client.Transport = usageRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		response, err := transport.RoundTrip(r)
		if err == nil {
			_ = response.Body.Close()
			return nil, errors.New("lost committed reply")
		}
		return response, err
	})
	sender, err := meter.NewRuntimeUsageHTTPSender(map[string]string{f.directory.IssuerID(): server.URL}, client)
	if err != nil {
		t.Fatal(err)
	}
	page, err := f.delivery(sender).DeliverPage(t.Context(), "", 0, 1)
	if err == nil || page.Imported != 0 || !reflect.DeepEqual(page.FailedIDs, []int64{item.ID}) {
		t.Fatal(page, err)
	}
	first, err := f.global.GlobalRuntimeUsage(t.Context(), f.usage.Grant.GlobalAppID, f.usage.Grant.AllocationID, f.cell.IssuerID(), time.UnixMicro(f.usage.MinuteMicros))
	if err != nil {
		t.Fatal("lost reply did not commit", err)
	}
	pending, err := f.regional.ListPendingRegionalRuntimeUsage(t.Context(), "", 0, 1)
	if err != nil || len(pending) != 1 {
		t.Fatal("lost reply hid pending usage", pending, err)
	}
	client.Transport = transport
	sender, err = meter.NewRuntimeUsageHTTPSender(map[string]string{f.directory.IssuerID(): server.URL}, client)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := f.delivery(sender).DeliverPage(t.Context(), "", 0, 1); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	final, err := f.global.GlobalRuntimeUsage(t.Context(), f.usage.Grant.GlobalAppID, f.usage.Grant.AllocationID, f.cell.IssuerID(), time.UnixMicro(f.usage.MinuteMicros))
	if err != nil || !reflect.DeepEqual(first, final) {
		t.Fatal("retry rewrote canonical receipt", final, err)
	}
	retained, err := f.regional.RegionalRuntimeUsageOutbox(t.Context(), f.usage.Grant.Binding.RegionalAppID, f.usage.Grant.AllocationID, time.UnixMicro(f.usage.MinuteMicros))
	if err != nil || retained.Ack == nil || retained.Ack.Usage != f.usage || retained.Ack.ImportedAtMicros != first.ImportedAt.UnixMicro() || retained.Envelope != item.Envelope {
		t.Fatal(retained, err)
	}
	pending, err = f.regional.ListPendingRegionalRuntimeUsage(t.Context(), "", 0, 1)
	if err != nil || len(pending) != 0 {
		t.Fatal(pending, err)
	}
	for _, check := range []struct {
		store   *state.MemStore
		account string
		want    int64
	}{{f.global, f.account.ID, f.usage.MBSeconds}, {f.regional, f.regionalAccount.ID, 0}} {
		rows, err := check.store.UsageByHour(t.Context(), check.account, time.UnixMicro(f.usage.MinuteMicros), f.now)
		if err != nil {
			t.Fatal(err)
		}
		var quantity int64
		for _, row := range rows {
			quantity += row.MBSeconds
		}
		if quantity != check.want {
			t.Fatal("wrong financial charge", quantity, check.want)
		}
	}
	if requests.Load() < 2 {
		t.Fatal("retry did not reach receiver")
	}
	rotated := f.receiver
	rotated.Signer, rotated.Directories = usageDeliverySigner(t, f.directory.IssuerID(), runtimequota.Directory)
	envelope, err := rotated.Receive(t.Context(), item.Envelope)
	if err != nil {
		t.Fatal(err)
	}
	ack, err := rotated.Directories.VerifyUsageImportAck(envelope)
	if err != nil || ack.Usage != f.usage || ack.ImportedAtMicros != first.ImportedAt.UnixMicro() {
		t.Fatal("rotation replaced original import", ack, err)
	}
}

func TestRegionalRuntimeUsageDeliveryFailedItemDoesNotStarveAndBadAckRetains(t *testing.T) {
	f := newUsageDeliveryFixture(t)
	first := f.queue(t, f.usage)
	laterUsage := f.usage
	laterUsage.MinuteMicros += time.Minute.Microseconds()
	laterUsage.ObservedAtMicros += time.Minute.Microseconds()
	second := f.queue(t, laterUsage)
	sender, _ := f.sender(t, f.receiver)
	failing := usageSenderFunc(func(ctx context.Context, row state.RegionalRuntimeUsageOutboxItem) (runtimequota.Envelope, error) {
		if row.ID == first.ID {
			return runtimequota.Envelope{}, errors.New("unavailable")
		}
		return sender.Send(ctx, row)
	})
	page, err := f.delivery(failing).DeliverPage(t.Context(), "", 0, 2)
	if err == nil || page.Imported != 1 || page.NextID != second.ID || !reflect.DeepEqual(page.FailedIDs, []int64{first.ID}) {
		t.Fatal(page, err)
	}
	empty, err := f.delivery(sender).DeliverPage(t.Context(), "", page.NextID, 2)
	if err != nil || empty.NextID != 0 {
		t.Fatal(empty, err)
	}
	wrong := runtimequota.UsageImportAck{Usage: laterUsage, ImportedAtMicros: f.now.UnixMicro()}
	wrongEnvelope, err := f.directory.SignUsageImportAck(wrong)
	if err != nil {
		t.Fatal(err)
	}
	bad := usageSenderFunc(func(context.Context, state.RegionalRuntimeUsageOutboxItem) (runtimequota.Envelope, error) {
		return wrongEnvelope, nil
	})
	if page, err := f.delivery(bad).DeliverPage(t.Context(), "", 0, 1); !errors.Is(err, state.ErrConflict) || page.Imported != 0 {
		t.Fatal(page, err)
	}
	wrongEnvelope.Signature = "untrusted"
	if page, err := f.delivery(bad).DeliverPage(t.Context(), "", 0, 1); err == nil || page.Imported != 0 {
		t.Fatal(page, err)
	}
	wrongEnvelope, err = f.directory.SignUsageImportAck(runtimequota.UsageImportAck{Usage: f.usage, ImportedAtMicros: f.now.Add(time.Second).UnixMicro()})
	if err != nil {
		t.Fatal(err)
	}
	if page, err := f.delivery(bad).DeliverPage(t.Context(), "", 0, 1); !errors.Is(err, state.ErrInvalidArgument) || page.Imported != 0 {
		t.Fatal("future import receipt accepted", page, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.delivery(sender).DeliverPage(ctx, "", 0, 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	pending, err := f.regional.ListPendingRegionalRuntimeUsage(t.Context(), "", 0, 2)
	if err != nil || len(pending) != 1 || pending[0].ID != first.ID {
		t.Fatal(pending, err)
	}
	if page, err := f.delivery(sender).DeliverPage(t.Context(), "", empty.NextID, 2); err != nil || page.Imported != 1 {
		t.Fatal(page, err)
	}
}

func TestRuntimeUsageHTTPSenderTrustBoundsAndRedirect(t *testing.T) {
	f := newUsageDeliveryFixture(t)
	item := f.queue(t, f.usage)
	for _, origin := range []string{"http://example.com", "https://user:secret@example.com", "https://example.com/path", "https://example.com?query", "https://example.com#fragment", "https://EXAMPLE.com", "https://example.com?", "https://:443", "https://example.com/"} {
		if _, err := meter.NewRuntimeUsageHTTPSender(map[string]string{f.directory.IssuerID(): origin}, nil); err == nil {
			t.Fatal("unsafe operator origin accepted", origin)
		}
	}
	var redirected atomic.Int64
	target := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
	defer target.Close()
	for _, test := range []struct {
		name    string
		handler http.Handler
	}{
		{"redirect", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
		})},
		{"oversized", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(strings.Repeat("x", api.GlobalRuntimeUsageAckEnvelopeMaxBytes+1)))
		})},
		{"noncanonical", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(" {}\n")) })},
	} {
		t.Run(test.name, func(t *testing.T) {
			sender, _ := f.sender(t, test.handler)
			if _, err := sender.Send(t.Context(), item); err == nil {
				t.Fatal("unsafe response accepted")
			}
		})
	}
	if redirected.Load() != 0 {
		t.Fatal("redirect forwarded usage")
	}
	sender, _ := f.sender(t, f.receiver)
	foreign := item
	foreign.Usage.Grant.IssuerID = uuid.NewString()
	if _, err := sender.Send(t.Context(), foreign); !errors.Is(err, runtimequota.ErrTrust) {
		t.Fatal(err)
	}
}

func TestGlobalRuntimeUsageReceiverHTTPValidationAndIssuerFence(t *testing.T) {
	f := newUsageDeliveryFixture(t)
	item := f.queue(t, f.usage)
	raw, err := json.Marshal(item.Envelope)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, method, body string
		status             int
	}{
		{"method", http.MethodGet, string(raw), http.StatusMethodNotAllowed},
		{"oversized", http.MethodPost, strings.Repeat("x", api.GlobalRuntimeUsageEnvelopeMaxBytes+1), http.StatusRequestEntityTooLarge},
		{"noncanonical", http.MethodPost, string(raw) + "\n", http.StatusBadRequest},
		{"missing", http.MethodPost, "{}", http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			f.receiver.ServeHTTP(w, httptest.NewRequest(test.method, meter.RuntimeUsageImportPath, strings.NewReader(test.body)))
			if w.Code != test.status {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
	receiver := f.receiver
	receiver.Signer, receiver.Directories = usageDeliverySigner(t, uuid.NewString(), runtimequota.Directory)
	if _, err := receiver.Receive(t.Context(), item.Envelope); !errors.Is(err, runtimequota.ErrBinding) {
		t.Fatal(err)
	}
	if _, err := f.global.GlobalRuntimeUsage(t.Context(), f.usage.Grant.GlobalAppID, f.usage.Grant.AllocationID, f.cell.IssuerID(), time.UnixMicro(f.usage.MinuteMicros)); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("wrong directory committed import", err)
	}
	receiver = f.receiver
	receiver.Cells = f.dirs
	if _, err := receiver.Receive(t.Context(), item.Envelope); err == nil {
		t.Fatal("directory trust accepted cell usage")
	}
}
