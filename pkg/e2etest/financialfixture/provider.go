// Package financialfixture supplies an isolated billing-provider boundary for
// cost-control acceptance. It never contacts an external provider.
package financialfixture

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
)

// Event retains canonical integer quantities; floating provider display units
// are never used to reconcile money or usage.
type Event struct {
	ExternalID string                     `json:"external_id"`
	Name       string                     `json:"name"`
	AccountID  string                     `json:"external_customer_id"`
	Timestamp  string                     `json:"timestamp"`
	Metadata   map[string]json.RawMessage `json:"metadata"`
}

func (e Event) Quantity() (int64, error) {
	key := "mb_seconds"
	if e.Name == "faas_egress_usage" {
		key = "egress_bytes"
	}
	var n int64
	if err := json.Unmarshal(e.Metadata[key], &n); err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid canonical quantity for %s", e.ExternalID)
	}
	return n, nil
}

type Snapshot struct {
	Events    map[string]Event `json:"events"`
	Attempts  map[string]int   `json:"attempts"`
	Conflicts []string         `json:"conflicts"`
}

// Provider emulates Polar's immutable external_id ingestion contract, including
// an accepted event whose response is lost. Outages remain until Recover.
type Provider struct {
	Server    *httptest.Server
	mu        sync.Mutex
	events    map[string]Event
	attempts  map[string]int
	conflicts []string
	fault     string
	price     int64
}

func NewProvider(t *testing.T, price int64) *Provider {
	t.Helper()
	p := &Provider{events: make(map[string]Event), attempts: make(map[string]int), price: price}
	p.Server = httptest.NewServer(http.HandlerFunc(p.serveHTTP))
	t.Cleanup(p.Server.Close)
	return p
}

func (p *Provider) FailBeforeAccept()    { p.setFault("before") }
func (p *Provider) LoseAcceptedReplies() { p.setFault("after") }
func (p *Provider) Recover()             { p.setFault("") }
func (p *Provider) setFault(f string)    { p.mu.Lock(); defer p.mu.Unlock(); p.fault = f }

func (p *Provider) Snapshot() Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	// JSON round-trip copies metadata too; callers cannot mutate the oracle.
	data, _ := json.Marshal(Snapshot{Events: p.events, Attempts: p.attempts, Conflicts: p.conflicts})
	var out Snapshot
	_ = json.Unmarshal(data, &out)
	return out
}

func (p *Provider) Env(from time.Time) []string {
	return []string{
		"FAAS_BILLING_PROVIDER=polar", "FAAS_POLAR_ACCESS_TOKEN=cost-acceptance-placeholder",
		"FAAS_POLAR_BASE_URL=" + p.Server.URL,
		"FAAS_POLAR_HOBBY_PRODUCT_ID=hobby", "FAAS_POLAR_PRO_PRODUCT_ID=pro", "FAAS_POLAR_SCALE_PRODUCT_ID=scale",
		"FAAS_POLAR_METER_ID=compute", "FAAS_POLAR_EGRESS_METER_ID=egress",
		"FAAS_POLAR_EGRESS_BILLING_MODE=live", "FAAS_POLAR_EGRESS_BILLING_FROM=" + from.UTC().Format(time.RFC3339),
		fmt.Sprintf("FAAS_POLAR_EGRESS_MILLICENTS_PER_GIB=%d", p.price),
		"FAAS_POLAR_HOBBY_INCLUDED_EGRESS_GIB=1", "FAAS_POLAR_PRO_INCLUDED_EGRESS_GIB=1", "FAAS_POLAR_SCALE_INCLUDED_EGRESS_GIB=1",
		"FAAS_STRIPE_INTERVAL=1s", "FAAS_QUOTA_INTERVAL=1h", "FAAS_DUNNING_INTERVAL=1h", "FAAS_RESIDENCY_INTERVAL=1s", "FAAS_SAMPLE_INTERVAL=1s",
	}
}

func (p *Provider) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != "Bearer cost-acceptance-placeholder" {
		http.Error(w, "fixture credential required", http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/products/") {
		id := strings.TrimPrefix(r.URL.Path, "/v1/products/")
		plan := api.Plan(id)
		if !plan.Valid() || plan == api.PlanFree {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": id, "recurring_interval": "month", "recurring_interval_count": 1, "is_recurring": true,
			"prices": []map[string]any{
				{"amount_type": "fixed", "price_currency": api.BillingCurrencyLower, "price_amount": billing.PlanMonthlyMillicents(plan) / api.MillicentsPerCent},
				{"amount_type": "metered_unit", "price_currency": api.BillingCurrencyLower, "unit_amount": "1", "meter_id": "compute"},
				{"amount_type": "metered_unit", "price_currency": api.BillingCurrencyLower, "unit_amount": fmt.Sprint(p.price / api.MillicentsPerCent), "meter_id": "egress"},
			}, "benefits": []any{},
		})
		return
	}
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/meters/") {
		id := strings.TrimPrefix(r.URL.Path, "/v1/meters/")
		event, property := "faas_ram_usage", "gb_ram_hours"
		if id == "egress" {
			event, property = "faas_egress_usage", "egress_gib"
		} else if id != "compute" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "unit": "scalar", "archived_at": nil, "filter": map[string]any{"conjunction": "and", "clauses": []map[string]string{{"property": "name", "operator": "eq", "value": event}}}, "aggregation": map[string]string{"func": "sum", "property": property}})
		return
	}
	if r.Method != http.MethodPost || r.URL.Path != "/v1/events/ingest" {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Events []Event `json:"events"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil || len(body.Events) != 1 {
		http.Error(w, "one event required", http.StatusBadRequest)
		return
	}
	e := body.Events[0]
	if _, err := e.Quantity(); err != nil || e.ExternalID == "" || e.AccountID == "" || (e.Name != "faas_ram_usage" && e.Name != "faas_egress_usage") {
		http.Error(w, "invalid event", http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.attempts[e.ExternalID]++
	if p.fault == "before" {
		http.Error(w, "provider unavailable before acceptance", http.StatusServiceUnavailable)
		return
	}
	old, duplicate := p.events[e.ExternalID]
	if duplicate && !reflect.DeepEqual(old, e) {
		p.conflicts = append(p.conflicts, e.ExternalID)
		http.Error(w, "external_id payload changed", http.StatusConflict)
		return
	}
	p.events[e.ExternalID] = e
	if p.fault == "after" {
		// Close after persisting acceptance, without an HTTP response. This
		// also exercises net/http's possible transport replay of keyed POSTs.
		conn, _, err := http.NewResponseController(w).Hijack()
		if err == nil {
			_ = conn.Close()
		}
		return
	}
	if duplicate {
		_, _ = w.Write([]byte(`{"inserted":0,"duplicates":1}`))
	} else {
		_, _ = w.Write([]byte(`{"inserted":1,"duplicates":0}`))
	}
}
