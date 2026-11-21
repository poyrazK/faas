package preview

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// ADR 20261115163113 §2: Track records conversion events for the request's
// verified customer and Flush sends them in batches of at most 100.
func newTrackTestFlags(t *testing.T, status, accepted int) (*GregaleFlags, func() [][]flagEvent) {
	t.Helper()
	identity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "test-workload-token"})
	}))
	t.Cleanup(identity.Close)
	var mu sync.Mutex
	var batches [][]flagEvent
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/runtime/flags/events" || r.Method != http.MethodPost ||
			r.Header.Get("Authorization") != "Bearer test-workload-token" || r.Header.Get("X-Faas-Flags-Capabilities") != flagSDKCapabilities {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		var body struct {
			Events []flagEvent `json:"events"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		batches = append(batches, body.Events)
		mu.Unlock()
		n := len(body.Events)
		if accepted >= 0 {
			n = min(n, accepted)
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]int{"accepted": n})
	}))
	t.Cleanup(api.Close)
	flags, err := NewGregaleFlags(GregaleFlagsOptions{
		APIURL: api.URL, IdentityEndpoint: identity.URL + "/identity", HTTPClient: api.Client(),
		Timeout: time.Second, Now: func() time.Time { return time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return flags, func() [][]flagEvent {
		mu.Lock()
		defer mu.Unlock()
		return batches
	}
}

func trackRequest(f *GregaleFlags, customer string) context.Context {
	h := http.Header{}
	if customer != "" {
		h.Set("X-Faas-Platform-Tenant-Id", customer)
	}
	return context.WithValue(context.Background(), flagRequestContextKey{}, f.newRequestState(h))
}

func TestTrackSendsCustomerNameValueAndTime(t *testing.T) {
	flags, sent := newTrackTestFlags(t, http.StatusOK, -1)
	ctx := trackRequest(flags, testFlagCustomer)
	if !flags.Track(ctx, "order_completed", 1200) || !flags.Track(ctx, "signup", 0) {
		t.Fatal("Track refused valid events")
	}
	if err := flags.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := [][]flagEvent{{
		{Name: "order_completed", CustomerID: testFlagCustomer, Value: 1200, At: "2026-11-15T12:00:00Z"},
		{Name: "signup", CustomerID: testFlagCustomer, At: "2026-11-15T12:00:00Z"},
	}}
	if got := sent(); !reflect.DeepEqual(got, want) {
		t.Fatalf("sent %+v; want %+v", got, want)
	}
	if flags.DroppedEvents() != 0 {
		t.Fatalf("dropped = %d", flags.DroppedEvents())
	}
}

func TestTrackDropsWithoutVerifiedCustomerOrValidInput(t *testing.T) {
	flags, sent := newTrackTestFlags(t, http.StatusOK, -1)
	if flags.Track(context.Background(), "signup", 0) {
		t.Error("tracked outside a request")
	}
	if flags.Track(trackRequest(flags, ""), "signup", 0) {
		t.Error("tracked without a verified customer")
	}
	ctx := trackRequest(flags, testFlagCustomer)
	if flags.Track(ctx, "Bad Name", 0) || flags.Track(ctx, "signup", 2_000_000_000_000) {
		t.Error("tracked an invalid name or value")
	}
	_ = flags.Flush(context.Background())
	if len(sent()) != 0 || flags.DroppedEvents() != 4 {
		t.Fatalf("sent %d batches, dropped %d; want none sent and 4 dropped", len(sent()), flags.DroppedEvents())
	}
}

// ADR 20261117084505 §1: events carry properties, the request's subject and
// every flag decision the request made before TrackWith.
func TestTrackWithSendsPropertiesSubjectAndRequestFlags(t *testing.T) {
	flags, sent := newTrackTestFlags(t, http.StatusOK, -1)
	ctx, err := flags.WithSubject(trackRequest(flags, testFlagCustomer), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := flags.Boolean(ctx, "new-checkout", false); err != nil {
		t.Fatal(err)
	}
	if _, err := flags.Variant(ctx, "pricing", "control"); err != nil {
		t.Fatal(err)
	}
	if !flags.TrackWith(ctx, "order_completed", TrackOptions{Properties: map[string]string{"plan": "pro"}}) ||
		!flags.TrackWith(ctx, "signup", TrackOptions{Subject: "user-2"}) {
		t.Fatal("TrackWith refused valid events")
	}
	tooMany := map[string]string{}
	for i := range 11 {
		tooMany[fmt.Sprintf("k%d", i)] = "v"
	}
	for _, props := range []map[string]string{{"Bad Key": "x"}, tooMany, {"plan": strings.Repeat("x", 257)}} {
		if flags.TrackWith(ctx, "signup", TrackOptions{Properties: props}) {
			t.Errorf("tracked invalid properties %v", props)
		}
	}
	if err := flags.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	decisions := map[string]string{"new-checkout": "false", "pricing": "control"}
	want := [][]flagEvent{{
		{Name: "order_completed", CustomerID: testFlagCustomer, At: "2026-11-15T12:00:00Z", Subject: "user-1", Properties: map[string]string{"plan": "pro"}, Flags: decisions},
		{Name: "signup", CustomerID: testFlagCustomer, At: "2026-11-15T12:00:00Z", Subject: "user-2", Flags: decisions},
	}}
	if got := sent(); !reflect.DeepEqual(got, want) {
		t.Fatalf("sent %+v; want %+v", got, want)
	}
	if flags.DroppedEvents() != 3 {
		t.Fatalf("dropped = %d; want 3", flags.DroppedEvents())
	}
}

func TestFlushBatchesAndCountsRefusedEvents(t *testing.T) {
	flags, sent := newTrackTestFlags(t, http.StatusOK, 90)
	ctx := trackRequest(flags, testFlagCustomer)
	for range 150 {
		flags.Track(ctx, "page_view", 0)
	}
	if err := flags.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := sent(); len(got) != 2 || len(got[0]) != 100 || len(got[1]) != 50 {
		t.Fatalf("batches = %d; want 100 then 50", len(got))
	}
	if flags.DroppedEvents() != 10 {
		t.Fatalf("dropped = %d; want the 10 refused of the first batch", flags.DroppedEvents())
	}

	down, _ := newTrackTestFlags(t, http.StatusServiceUnavailable, -1)
	down.Track(trackRequest(down, testFlagCustomer), "signup", 0)
	if err := down.Flush(context.Background()); err == nil || down.DroppedEvents() != 1 {
		t.Fatalf("flush against a failing API = %v, dropped %d", err, down.DroppedEvents())
	}
}
