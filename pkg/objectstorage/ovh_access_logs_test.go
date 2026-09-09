package objectstorage

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

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
			"gregale/one.log": `owner bucket-a [01/Jan/2026:00:00:01 +0000] - request-1 GET - "a" 200 - 1 1 1 1 "-" "agent" - - -` + "\n" +
				`owner bucket-a [01/Jan/2026:00:00:02 +0000] - request-2 GET - "b" 200 - 1 1 1 1 "-" "agent" - - -` + "\n" +
				`owner bucket-b [31/Dec/2025:23:59:59 +0000] - request-3 GET - "c" 200 - 1 1 1 1 "-" "agent" - - -` + "\n",
			"gregale/two.log": `owner bucket-b [01/Jan/2026:00:00:03 +0000] - request-4 GET - "d" 200 - 1 1 1 1 "-" "agent" - - -` + "\n" +
				`owner other [01/Jan/2026:00:00:04 +0000] - request-5 GET - "e" 200 - 1 1 1 1 "-" "agent" - - -` + "\n",
		},
	}
	metrics, err := (OVHAccessLogRequestMetrics{
		Store: store, LogBucket: "logs", LogPrefix: "gregale/",
	}).Metrics(context.Background(), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 0, 0, 10, 0, time.UTC), []OVHUsageBucket{
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
	if _, _, ok := parseOVHAccessLogLine(`owner bucket [not-a-time] - request GET - "x"`); ok {
		t.Fatal("malformed access-log record parsed successfully")
	}
}

func TestOVHAccessLogRequestMetricsRejectsEmptyLogBucket(t *testing.T) {
	_, err := (OVHAccessLogRequestMetrics{
		Store: fakeAccessLogStore{pages: []ObjectPage{{}}}, LogBucket: "logs",
	}).Metrics(context.Background(), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC), []OVHUsageBucket{{AccountID: "acct", PhysicalName: "bucket"}})
	if err != ErrOVHRequestMetricsMissing {
		t.Fatalf("error = %v, want %v", err, ErrOVHRequestMetricsMissing)
	}
}
