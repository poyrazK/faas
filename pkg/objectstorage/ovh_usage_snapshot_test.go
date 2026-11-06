package objectstorage

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Schema fixture: cloud.billingView.HourlyStorage.totalPrice is a number;
// only the enclosing document's totalPrice is order.Price (OVH cloud.json).
func ovhNativeUsageDocument(period, updated time.Time) map[string]any {
	return map[string]any{
		"id": "month", "period": map[string]any{"from": period, "to": period.AddDate(0, 1, 0)}, "lastUpdate": updated,
		"totalPrice": map[string]any{"currencyCode": "EUR", "priceInUcents": 125000000},
		"hourlyUsage": map[string]any{"storage": []any{map[string]any{
			"bucketName": "gregale-a", "stored": map[string]any{"quantity": map[string]any{"value": 3, "unit": "GiBh"}},
			"outgoingBandwidth": map[string]any{"quantity": map[string]any{"value": 4, "unit": "GiB"}}, "totalPrice": json.Number("1.25"),
		}}},
	}
}

func ovhNativeClient(t *testing.T, handler http.HandlerFunc) *OVHUsageClient {
	t.Helper()
	native := httptest.NewTLSServer(handler)
	t.Cleanup(native.Close)
	return &OVHUsageClient{BaseURL: native.URL, ServiceName: "project", ApplicationKey: "synthetic", ApplicationSecret: "synthetic", ConsumerKey: "synthetic", HTTPClient: native.Client(), Timestamp: func(context.Context) (int64, error) { return 1, nil }}
}

func TestOVHCurrentUsageRetainsNativeWatermark(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	period := utcMonth(now)
	observed := now.Add(-time.Hour)
	if observed.Before(period) {
		observed = period
	}
	document := ovhNativeUsageDocument(period, observed)
	calls := 0
	client := ovhNativeClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if !strings.HasSuffix(r.URL.Path, "/usage/current") || r.URL.RawQuery != "" {
			t.Error("wrong live usage feed", r.URL)
		}
		_ = json.NewEncoder(w).Encode(document)
	})
	metricsAt := time.Time{}
	exporter := OVHUsageExporter{API: client, Catalog: func(context.Context, string, string) ([]OVHUsageBucket, error) {
		return []OVHUsageBucket{{AccountID: "account", PhysicalName: "gregale-a"}}, nil
	}, RequestMetrics: func(_ context.Context, _ string, start, at time.Time) ([]OVHRequestMetric, error) {
		metricsAt = at
		return []OVHRequestMetric{{PhysicalName: "gregale-a", Count: 7}}, nil
	}}
	path := filepath.Join(t.TempDir(), "usage.json")
	if err := ExportUsageReports(t.Context(), path, exporter, UsageReportExportRequest{BackendID: "backend", BackendFingerprint: "fingerprint", PeriodStart: period, ObservedAt: now}); err != nil {
		t.Fatal(err)
	}
	rows, err := readUsageReports(path)
	if err != nil || len(rows) != 1 || !rows[0].ObservedAt.Equal(observed) || !metricsAt.Equal(observed) || rows[0].CostMillicents != 125000 || rows[0].StoredByteHours != 3*ovhGiBBytes || calls != 1 {
		t.Fatal("watermark or native quantities lost", rows, metricsAt, calls, err)
	}
}

func TestOVHUsageRejectsMissingOrWrongEvidence(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	period := utcMonth(now)
	for _, kind := range []string{"missing bucket", "null body", "missing watermark", "future watermark", "past watermark", "wrong period", "period too short", "USD", "missing currency", "missing price", "null price", "object price", "missing hourly", "missing storage"} {
		t.Run(kind, func(t *testing.T) {
			doc := ovhNativeUsageDocument(period, now)
			storage := doc["hourlyUsage"].(map[string]any)["storage"].([]any)[0].(map[string]any)
			switch kind {
			case "missing bucket":
				doc["hourlyUsage"].(map[string]any)["storage"] = []any{}
			case "null body":
				doc = nil
			case "missing watermark":
				delete(doc, "lastUpdate")
			case "future watermark":
				doc["lastUpdate"] = now.Add(time.Hour)
			case "past watermark":
				doc["lastUpdate"] = period.Add(-time.Second)
			case "wrong period":
				doc["period"].(map[string]any)["from"] = period.AddDate(0, -1, 0)
			case "period too short":
				doc["period"].(map[string]any)["to"] = period
			case "USD":
				doc["totalPrice"].(map[string]any)["currencyCode"] = "USD"
			case "missing currency":
				delete(doc, "totalPrice")
			case "missing price":
				delete(storage, "totalPrice")
			case "null price":
				storage["totalPrice"] = nil
			case "object price":
				storage["totalPrice"] = map[string]any{"currencyCode": "EUR", "priceInUcents": 1}
			case "missing hourly":
				delete(doc, "hourlyUsage")
			case "missing storage":
				delete(doc["hourlyUsage"].(map[string]any), "storage")
			}
			client := ovhNativeClient(t, func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(doc) })
			exporter := OVHUsageExporter{API: client, Catalog: func(context.Context, string, string) ([]OVHUsageBucket, error) {
				return []OVHUsageBucket{{AccountID: "account", PhysicalName: "gregale-a"}}, nil
			}, RequestMetrics: func(context.Context, string, time.Time, time.Time) ([]OVHRequestMetric, error) {
				return []OVHRequestMetric{{PhysicalName: "gregale-a", Count: 0}}, nil
			}}
			path := filepath.Join(t.TempDir(), "usage.json")
			previous := []byte("previous complete report\n")
			if err := os.WriteFile(path, previous, 0600); err != nil {
				t.Fatal(err)
			}
			err := ExportUsageReports(t.Context(), path, exporter, UsageReportExportRequest{BackendID: "backend", BackendFingerprint: "fingerprint", PeriodStart: period, ObservedAt: now})
			data, readErr := os.ReadFile(path)
			if !errors.Is(err, ErrOVHUsageInvalid) || readErr != nil || string(data) != string(previous) {
				t.Fatal("invalid financial evidence published", err, readErr, string(data))
			}
		})
	}
}

func TestOVHClosedUsageRequiresCompletePeriod(t *testing.T) {
	now := time.Now().UTC()
	period := utcMonth(now).AddDate(0, -1, 0)
	end := period.AddDate(0, 1, 0)
	for _, kind := range []string{"valid", "empty history", "null history", "duplicate", "two snapshots", "partial period", "stale finalization", "wrong detail", "missing detail watermark", "partial request"} {
		t.Run(kind, func(t *testing.T) {
			entry := map[string]any{"id": "month", "period": map[string]any{"from": period, "to": end}, "lastUpdate": end}
			var history any = []any{entry}
			detail := ovhNativeUsageDocument(period, end)
			through := end.Add(-time.Microsecond)
			switch kind {
			case "empty history":
				history = []any{}
			case "null history":
				history = nil
			case "duplicate":
				history = []any{entry, entry}
			case "two snapshots":
				other := map[string]any{"id": "another", "period": entry["period"], "lastUpdate": end}
				history = []any{entry, other}
			case "partial period":
				entry["period"].(map[string]any)["from"] = period.Add(time.Hour)
			case "stale finalization":
				entry["lastUpdate"] = end.Add(-time.Hour)
			case "wrong detail":
				detail["id"] = "other"
			case "missing detail watermark":
				delete(detail, "lastUpdate")
			case "partial request":
				through = period.Add(time.Hour)
			}
			client := ovhNativeClient(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/usage/history") {
					_ = json.NewEncoder(w).Encode(history)
				} else if strings.HasSuffix(r.URL.Path, "/usage/history/month") {
					_ = json.NewEncoder(w).Encode(detail)
				} else {
					t.Error("wrong historical feed", r.URL)
					w.WriteHeader(500)
				}
			})
			snapshot, err := client.UsageForPeriod(t.Context(), period, through)
			if kind == "valid" {
				if err != nil || !snapshot.ObservedAt.Equal(through) || snapshot.Buckets["gregale-a"].CostMillicents != 125000 {
					t.Fatal(snapshot, err)
				}
			} else if !errors.Is(err, ErrOVHUsageInvalid) {
				t.Fatal("ambiguous historical proof accepted", snapshot, err)
			}
		})
	}
}

func TestOVHNativeDecimalMoney(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int64
		bad   bool
	}{
		{"0", 0, false}, {"1.25", 125000, false}, {"0.00001", 1, false}, {"0.00001000000000000000001", 2, false}, {"1e-100", 1, false}, {"1.25e1", 1250000, false}, {"0.000019", 2, false},
		{"null", 0, true}, {`"1.25"`, 0, true}, {"-1", 0, true}, {"1e100", 0, true}, {"1e999999999999999", 0, true}, {"{}", 0, true}, {"1.25x", 0, true},
	} {
		t.Run(tc.value, func(t *testing.T) {
			got, err := priceToMillicents(json.RawMessage(tc.value))
			if tc.bad {
				if err == nil {
					t.Fatal("invalid money accepted", got)
				}
			} else if err != nil || got != tc.want {
				t.Fatal(got, err)
			}
		})
	}
}
