package objectstorage

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strconv"
	"strings"
	"testing"
	"time"
)

type fakeOVHUsageAPI map[string]OVHBucketUsage

func (f fakeOVHUsageAPI) UsageForPeriod(_ context.Context, from, observed time.Time) (OVHUsageSnapshot, error) {
	return OVHUsageSnapshot{PeriodStart: from, ObservedAt: observed, Buckets: f}, nil
}

func TestOVHUsageExporterAggregatesBucketsAndRequests(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	period := utcMonth(now)
	exporter := OVHUsageExporter{
		API: fakeOVHUsageAPI{
			"gregale-a": {StoredByteHours: 10, EgressBytes: 20, CostMillicents: 30},
			"gregale-b": {StoredByteHours: 4, EgressBytes: 5, CostMillicents: 6},
		},
		Catalog: func(context.Context, string, string) ([]OVHUsageBucket, error) {
			return []OVHUsageBucket{{AccountID: "account", PhysicalName: "gregale-a"}, {AccountID: "account", PhysicalName: "gregale-b"}}, nil
		},
		RequestMetrics: func(context.Context, string, time.Time, time.Time) ([]OVHRequestMetric, error) {
			return []OVHRequestMetric{{PhysicalName: "gregale-a", Count: 7}, {PhysicalName: "gregale-b", Count: 8}}, nil
		},
	}
	reports, err := exporter.ExportUsageReports(context.Background(), UsageReportExportRequest{
		BackendID:          "ovh-us-east",
		BackendFingerprint: "fingerprint",
		PeriodStart:        period,
		ObservedAt:         now,
	})
	if err != nil {
		t.Fatalf("ExportUsageReports() error = %v", err)
	}
	if len(reports) != 1 {
		t.Fatalf("reports = %#v, want one account row", reports)
	}
	got := reports[0]
	if got.AccountID != "account" || got.StoredByteHours != 14 || got.RequestCount != 15 || got.EgressBytes != 25 || got.CostMillicents != 36 {
		t.Fatalf("report = %#v, want aggregated measurements", got)
	}
	if got.Source != "ovh-public-cloud" || !got.PeriodStart.Equal(period) || !got.ObservedAt.Equal(now) {
		t.Fatalf("report identity = %#v", got)
	}
}

func TestOVHUsageExporterFailsClosedWithoutCompleteRequestMetrics(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	period := utcMonth(now)
	exporter := OVHUsageExporter{
		API: fakeOVHUsageAPI{"gregale-a": {}},
		Catalog: func(context.Context, string, string) ([]OVHUsageBucket, error) {
			return []OVHUsageBucket{{AccountID: "account", PhysicalName: "gregale-a"}}, nil
		},
		RequestMetrics: func(context.Context, string, time.Time, time.Time) ([]OVHRequestMetric, error) {
			return nil, nil
		},
	}
	_, err := exporter.ExportUsageReports(context.Background(), UsageReportExportRequest{BackendID: "backend", BackendFingerprint: "fingerprint", PeriodStart: period, ObservedAt: now})
	if !errors.Is(err, ErrOVHRequestMetricsMissing) {
		t.Fatalf("error = %v, want ErrOVHRequestMetricsMissing", err)
	}
}

func TestOVHUsageExporterRejectsUnknownProviderBucket(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	period := utcMonth(now)
	exporter := OVHUsageExporter{
		API: fakeOVHUsageAPI{"unowned": {}},
		Catalog: func(context.Context, string, string) ([]OVHUsageBucket, error) {
			return []OVHUsageBucket{{AccountID: "account", PhysicalName: "gregale-a"}}, nil
		},
		RequestMetrics: func(context.Context, string, time.Time, time.Time) ([]OVHRequestMetric, error) {
			return []OVHRequestMetric{{PhysicalName: "gregale-a", Count: 1}}, nil
		},
	}
	_, err := exporter.ExportUsageReports(context.Background(), UsageReportExportRequest{BackendID: "backend", BackendFingerprint: "fingerprint", PeriodStart: period, ObservedAt: now})
	if !errors.Is(err, ErrOVHUsageInvalid) {
		t.Fatalf("error = %v, want ErrOVHUsageInvalid", err)
	}
}

func TestOVHUsageClientFetchesAndSignsHistory(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	period := utcMonth(now).AddDate(0, -1, 0)
	const applicationSecret = "application-secret"
	const consumerKey = "consumer-key"
	const timestamp = int64(1728000000)
	var requests []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.String())
		if got := r.Header.Get("X-Ovh-Application"); got != "application-key" {
			t.Errorf("application header = %q", got)
		}
		if got := r.Header.Get("X-Ovh-Consumer"); got != consumerKey {
			t.Errorf("consumer header = %q", got)
		}
		date := strconv.FormatInt(timestamp, 10)
		signatureInput := strings.Join([]string{applicationSecret, consumerKey, date, http.MethodGet, "https://" + r.Host + r.URL.RequestURI(), ""}, "+")
		sum := sha1.Sum([]byte(signatureInput))
		if got, want := r.Header.Get("X-Ovh-Signature"), "$1$"+hex.EncodeToString(sum[:]); got != want {
			t.Errorf("signature = %q, want %q", got, want)
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/usage/history"):
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "month", "period": map[string]any{"from": period, "to": period.AddDate(0, 1, 0)}, "lastUpdate": period.AddDate(0, 1, 0)}})
		case strings.HasSuffix(r.URL.Path, "/usage/history/month"):
			_ = json.NewEncoder(w).Encode(ovhNativeUsageDocument(period, period.AddDate(0, 1, 0)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := OVHUsageClient{
		BaseURL:           server.URL + "/1.0",
		ServiceName:       "project-id",
		ApplicationKey:    "application-key",
		ApplicationSecret: applicationSecret,
		ConsumerKey:       consumerKey,
		HTTPClient:        server.Client(),
		Timestamp: func(context.Context) (int64, error) {
			return timestamp, nil
		},
	}
	usage, err := client.UsageForPeriod(context.Background(), period, now)
	if err != nil {
		t.Fatalf("UsageForPeriod() error = %v", err)
	}
	got := usage.Buckets["gregale-a"]
	if got.StoredByteHours != 3*ovhGiBBytes || got.EgressBytes != 4*ovhGiBBytes || got.CostMillicents != 125000 {
		t.Fatalf("usage = %#v, want normalized values", got)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %v, want history plus one complete monthly detail", requests)
	}
	parsed, err := url.Parse(requests[0])
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != path.Join("/1.0", "/cloud/project/"+url.PathEscape("project-id")+"/usage/history") {
		t.Fatalf("history path = %q", parsed.Path)
	}
}

var _ UsageReportExporter = OVHUsageExporter{}
var _ OVHUsageAPI = (*OVHUsageClient)(nil)
