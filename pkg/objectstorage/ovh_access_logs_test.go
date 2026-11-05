package objectstorage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func accessLogMetricsFixture(t *testing.T, store fakeAccessLogStore, period, through time.Time, buckets ...string) OVHAccessLogRequestMetrics {
	t.Helper()
	coverage := OVHAccessLogCoverage{Version: 1, BackendID: "backend", BackendFingerprint: "fingerprint", LogBucket: "logs", LogPrefix: "gregale/", PeriodStart: period, CoveredThrough: through, Buckets: buckets, Objects: []OVHAccessLogObject{}}
	for _, page := range store.pages {
		for _, object := range page.Items {
			digest := sha256.Sum256([]byte(store.logs[object.Key]))
			coverage.Objects = append(coverage.Objects, OVHAccessLogObject{Key: object.Key, SHA256: hex.EncodeToString(digest[:])})
		}
	}
	return OVHAccessLogRequestMetrics{Store: store, BackendID: coverage.BackendID, BackendFingerprint: coverage.BackendFingerprint, LogBucket: coverage.LogBucket, LogPrefix: coverage.LogPrefix, Coverage: func(context.Context, time.Time) (OVHAccessLogCoverage, error) { return coverage, nil }}
}

type fakeAccessLogStore struct {
	pages []ObjectPage
	logs  map[string]string
}

func (f fakeAccessLogStore) ListObjects(_ context.Context, _, _, cursor string, _ int32) (ObjectPage, error) {
	if len(f.pages) == 0 {
		return ObjectPage{}, nil
	}
	if cursor != "" && len(f.pages) > 1 {
		return f.pages[1], nil
	}
	return f.pages[0], nil
}

func (f fakeAccessLogStore) ReadObject(_ context.Context, _, key string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(f.logs[key])), nil
}

func TestOVHAccessLogRequestMetricsCountsCatalogBuckets(t *testing.T) {
	store := fakeAccessLogStore{
		pages: []ObjectPage{{Items: []Object{{Key: "gregale/one.log"}}, NextCursor: "next"}, {Items: []Object{{Key: "gregale/two.log"}}}},
		logs: map[string]string{
			"gregale/one.log": `owner bucket-a [01/Jan/2026:00:00:01 +0000] - requester request-1 REST.GET.OBJECT a "GET /a HTTP/1.1" 200 - 1 1 1 1 "-" "agent" - - -` + "\n" +
				`owner bucket-a [01/Jan/2026:00:00:02 +0000] - requester request-2 REST.GET.OBJECT b "GET /b HTTP/1.1" 200 - 1 1 1 1 "-" "agent" - - -` + "\n" +
				`owner bucket-b [31/Dec/2025:23:59:59 +0000] - requester request-3 REST.GET.OBJECT c "GET /c HTTP/1.1" 200 - 1 1 1 1 "-" "agent" - - -` + "\n",
			"gregale/two.log": `owner bucket-b [01/Jan/2026:00:00:03 +0000] - requester request-4 REST.GET.OBJECT d "GET /d HTTP/1.1" 200 - 1 1 1 1 "-" "agent" - - -` + "\n" +
				`owner other [01/Jan/2026:00:00:04 +0000] - requester request-5 REST.GET.OBJECT e "GET /e HTTP/1.1" 200 - 1 1 1 1 "-" "agent" - - -` + "\n",
		},
	}
	period, through := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 0, 0, 10, 0, time.UTC)
	source := accessLogMetricsFixture(t, store, period, through, "bucket-a", "bucket-b")
	metrics, err := source.Metrics(t.Context(), period, through, []OVHUsageBucket{
		{AccountID: "acct-a", PhysicalName: "bucket-a"},
		{AccountID: "acct-b", PhysicalName: "bucket-b"},
	})
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	if len(metrics) != 2 || metrics[0] != (OVHRequestMetric{PhysicalName: "bucket-a", Count: 2}) || metrics[1] != (OVHRequestMetric{PhysicalName: "bucket-b", Count: 1}) {
		t.Fatalf("metrics = %#v", metrics)
	}
}

func TestParseOVHAccessLogLineRejectsMalformedRecord(t *testing.T) {
	if _, ok := parseOVHAccessLogLine(`owner bucket [not-a-time] - requester request GET - "x" 200`); ok {
		t.Fatal("malformed access-log record parsed successfully")
	}
}

func TestOVHAccessLogRequestMetricsRejectsEmptyLogBucket(t *testing.T) {
	_, err := (OVHAccessLogRequestMetrics{
		Store: fakeAccessLogStore{pages: []ObjectPage{{}}}, LogBucket: "logs",
	}).Metrics(context.Background(), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC), []OVHUsageBucket{{AccountID: "acct", PhysicalName: "bucket"}})
	if !errors.Is(err, ErrOVHRequestMetricsMissing) {
		t.Fatalf("error = %v, want %v", err, ErrOVHRequestMetricsMissing)
	}
}
