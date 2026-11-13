package objectstorage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func accessLogLine(bucket, request string) string {
	return `owner ` + bucket + ` [02/Jan/2026:12:00:00 +0000] 127.0.0.1 requester ` + request + ` REST.GET.OBJECT key "GET /key HTTP/1.1" 200 - 1 1 1 1 "-" "agent" -` + "\n"
}

func TestOVHAccessLogCoverageAndDuplicateRequests(t *testing.T) {
	period := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	through := period.Add(48 * time.Hour)
	for _, tc := range []struct {
		name, first, second string
		count               int64
		invalid             bool
	}{
		{"distinct", accessLogLine("bucket", "request-a"), accessLogLine("bucket", "request-b"), 2, false},
		{"duplicate", accessLogLine("bucket", "request-a"), accessLogLine("bucket", "request-a"), 1, false},
		{"conflicting duplicate", accessLogLine("bucket", "request-a"), strings.Replace(accessLogLine("bucket", "request-a"), "200 -", "403 AccessDenied", 1), 0, true},
		{"missing identity", accessLogLine("bucket", "-"), "", 0, true},
		{"explicit quiet bucket", "", "", 0, false},
		{"unrelated records with proven quiet bucket", accessLogLine("other", "request-a"), "", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := fakeAccessLogStore{pages: []ObjectPage{{Items: []Object{{Key: "gregale/a"}, {Key: "gregale/b"}}}}, logs: map[string]string{"gregale/a": tc.first, "gregale/b": tc.second}}
			source := accessLogMetricsFixture(t, store, period, through, "bucket")
			source.MaxRecords = 2
			if tc.name == "duplicate" {
				source.MaxRecords = 1
			}
			metrics, err := source.Metrics(t.Context(), period, through, []OVHUsageBucket{{AccountID: "account", PhysicalName: "bucket"}})
			if tc.invalid {
				if !errors.Is(err, ErrOVHUsageInvalid) || len(metrics) != 0 {
					t.Fatal("invalid request evidence published", metrics, err)
				}
			} else if err != nil || len(metrics) != 1 || metrics[0].Count != tc.count {
				t.Fatal("unique request count", metrics, err)
			}
		})
	}
}

func TestOVHAccessLogRequestIdentityIsScopedToBucket(t *testing.T) {
	period := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	through := period.Add(48 * time.Hour)
	store := fakeAccessLogStore{pages: []ObjectPage{{Items: []Object{{Key: "gregale/a"}}}}, logs: map[string]string{"gregale/a": accessLogLine("bucket-a", "same-id") + accessLogLine("bucket-b", "same-id")}}
	source := accessLogMetricsFixture(t, store, period, through, "bucket-a", "bucket-b")
	metrics, err := source.Metrics(t.Context(), period, through, []OVHUsageBucket{{AccountID: "account", PhysicalName: "bucket-a"}, {AccountID: "account", PhysicalName: "bucket-b"}})
	if err != nil || len(metrics) != 2 || metrics[0].Count != 1 || metrics[1].Count != 1 {
		t.Fatal("bucket identity was lost", metrics, err)
	}
}

func TestOVHAccessLogRejectsUnqualifiedOrChangedCoverage(t *testing.T) {
	period := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	through := period.Add(48 * time.Hour)
	for _, kind := range []string{"none", "old logs", "unrelated logs", "stale", "wrong backend", "wrong placement", "wrong log bucket", "wrong prefix", "wrong month", "future", "missing bucket", "duplicate bucket", "missing object set", "wrong digest", "duplicate object", "outside prefix", "object bound", "record bound"} {
		t.Run(kind, func(t *testing.T) {
			store := fakeAccessLogStore{pages: []ObjectPage{{Items: []Object{{Key: "gregale/a"}, {Key: "gregale/b"}}}}, logs: map[string]string{"gregale/a": accessLogLine("bucket", "a"), "gregale/b": accessLogLine("bucket", "b")}}
			source := accessLogMetricsFixture(t, store, period, through, "bucket")
			coverage, err := source.Coverage(t.Context(), period)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "none", "old logs", "unrelated logs":
				source.Coverage = nil
			case "stale":
				coverage.CoveredThrough = through.Add(-time.Second)
			case "wrong backend":
				coverage.BackendID = "other"
			case "wrong placement":
				coverage.BackendFingerprint = "other"
			case "wrong log bucket":
				coverage.LogBucket = "other"
			case "wrong prefix":
				coverage.LogPrefix = "other/"
			case "wrong month":
				coverage.PeriodStart = period.AddDate(0, -1, 0)
			case "future":
				coverage.CoveredThrough = time.Now().Add(time.Hour)
			case "missing bucket":
				coverage.Buckets = nil
			case "duplicate bucket":
				coverage.Buckets = []string{"bucket", "bucket"}
			case "missing object set":
				coverage.Objects = nil
			case "wrong digest":
				coverage.Objects[0].SHA256 = strings.Repeat("0", 64)
			case "duplicate object":
				coverage.Objects[1] = coverage.Objects[0]
			case "outside prefix":
				coverage.Objects[0].Key = "other/a"
			case "object bound":
				source.MaxObjects = 1
			case "record bound":
				source.MaxRecords = 1
			}
			if kind == "old logs" {
				store.logs["gregale/a"] = strings.Replace(accessLogLine("bucket", "old"), "02/Jan/2026", "31/Dec/2025", 1)
			}
			if kind == "unrelated logs" {
				store.logs["gregale/a"] = accessLogLine("other", "unrelated")
			}
			if source.Coverage != nil {
				source.Coverage = func(context.Context, time.Time) (OVHAccessLogCoverage, error) { return coverage, nil }
			}
			metrics, err := source.Metrics(t.Context(), period, through, []OVHUsageBucket{{AccountID: "account", PhysicalName: "bucket"}})
			if err == nil || len(metrics) != 0 {
				t.Fatal("unqualified evidence published", metrics, err)
			}
		})
	}
}

func TestOVHAccessLogCoverageFileAndFailedPublication(t *testing.T) {
	period := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	through := period.Add(48 * time.Hour)
	source := accessLogMetricsFixture(t, fakeAccessLogStore{}, period, through, "bucket")
	c, err := source.Coverage(t.Context(), period)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "coverage.json")
	write := func(body any) {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write([]OVHAccessLogCoverage{c})
	source.Coverage = ovhAccessLogCoverageFile(path)
	metrics, err := source.Metrics(t.Context(), period, through, []OVHUsageBucket{{AccountID: "account", PhysicalName: "bucket"}})
	if err != nil || len(metrics) != 1 || metrics[0].Count != 0 {
		t.Fatal("qualified zero rejected", metrics, err)
	}
	write([]OVHAccessLogCoverage{c, c})
	if _, err = source.Coverage(t.Context(), period); !errors.Is(err, ErrOVHUsageInvalid) {
		t.Fatal("duplicate month accepted", err)
	}
	if err = os.WriteFile(path, []byte(`[] {}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = source.Coverage(t.Context(), period); !errors.Is(err, ErrOVHUsageInvalid) {
		t.Fatal("trailing document accepted", err)
	}
	write([]OVHAccessLogCoverage{c})
	if err = os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err = source.Coverage(t.Context(), period); !errors.Is(err, ErrOVHRequestMetricsMissing) {
		t.Fatal("writable evidence accepted", err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	previous := []byte("previous complete report\n")
	report := filepath.Join(t.TempDir(), "usage.json")
	if err = os.WriteFile(report, previous, 0o600); err != nil {
		t.Fatal(err)
	}
	exporter := OVHUsageExporter{API: fakeOVHUsageAPI{"bucket": {}}, Catalog: func(context.Context, string, string) ([]OVHUsageBucket, error) {
		return []OVHUsageBucket{{AccountID: "account", PhysicalName: "bucket"}}, nil
	}, RequestMetrics: func(ctx context.Context, _ string, start, observed time.Time) ([]OVHRequestMetric, error) {
		return source.Metrics(ctx, start, observed, []OVHUsageBucket{{AccountID: "account", PhysicalName: "bucket"}})
	}}
	err = ExportUsageReports(t.Context(), report, exporter, UsageReportExportRequest{BackendID: "backend", BackendFingerprint: "fingerprint", PeriodStart: period, ObservedAt: through})
	if !errors.Is(err, ErrOVHRequestMetricsMissing) {
		t.Fatal("missing evidence published", err)
	}
	got, err := os.ReadFile(report)
	if err != nil || string(got) != string(previous) {
		t.Fatal("failure replaced previous report", string(got), err)
	}
}
