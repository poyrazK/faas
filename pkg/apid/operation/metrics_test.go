// adr: 20261016123221
package operation

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
)

func TestWebhookOutcomeWriterClassifiesReceipts(t *testing.T) {
	for name, tc := range map[string]struct {
		write func(http.ResponseWriter)
		want  string
	}{
		"accepted": {func(w http.ResponseWriter) {
			httpkit.WriteJSON(w, http.StatusAccepted, api.InboundWebhookReceiptResponse{Status: "accepted"})
		}, "accepted"},
		"replay": {func(w http.ResponseWriter) {
			httpkit.WriteJSON(w, http.StatusAccepted, api.InboundWebhookReceiptResponse{Status: "accepted", Duplicate: true})
		}, "duplicate"},
		"ignored": {func(w http.ResponseWriter) {
			httpkit.WriteJSON(w, http.StatusAccepted, api.InboundWebhookReceiptResponse{Status: "ignored"})
		}, "ignored"},
		"signaled": {func(w http.ResponseWriter) {
			httpkit.WriteJSON(w, http.StatusAccepted, api.InboundWebhookReceiptResponse{Status: api.InboundWebhookSignaled})
		}, "signaled"},
		"unmatched": {func(w http.ResponseWriter) {
			httpkit.WriteJSON(w, http.StatusAccepted, api.InboundWebhookReceiptResponse{Status: api.InboundWebhookUnmatched})
		}, "unmatched"},
		"refused": {func(w http.ResponseWriter) {
			api.WriteProblem(w, api.ErrCapacity("operation signal store unavailable"))
		}, "refused"},
		"too large": {func(w http.ResponseWriter) { api.WriteProblem(w, api.ErrRequestBodyTooLarge(1, 2)) }, "refused"},
	} {
		recorder := &webhookOutcomeWriter{ResponseWriter: httptest.NewRecorder()}
		tc.write(recorder)
		if got := recorder.result(); got != tc.want {
			t.Errorf("%s: result %q, want %q", name, got, tc.want)
		}
	}
}

func TestMetricsCountWebhookEventsOnce(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg, "apid")
	if again := NewMetrics(reg, "apid"); again.webhookEvents != m.webhookEvents {
		t.Fatal("a second registration made a second counter")
	}
	m.webhookEvent(api.OperationWebhookModeSignal, "unmatched")
	m.webhookEvent(api.OperationWebhookModeSignal, "unmatched")
	if got := testutil.ToFloat64(m.webhookEvents.WithLabelValues("signal", "unmatched")); got != 2 {
		t.Fatalf("unmatched signals counted %v", got)
	}
	var none *Metrics
	none.webhookEvent("start", "accepted")
	if none.Registry() != nil {
		t.Fatal("nil metrics reported a registry")
	}
}

// ADR-20261018210500: the queue gauges report the fleet backlog; a second
// registration reuses them, and an empty queue reads zero age.
func TestMetricsReportOperationQueue(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg, "apid")
	if again := NewMetrics(reg, "apid"); again.queued != m.queued || again.queueOldest != m.queueOldest || again.queueReleases != m.queueReleases {
		t.Fatal("a second registration made second queue metrics")
	}
	now := time.Now()
	oldest := now.Add(-90 * time.Second)
	m.operationQueue(3, &oldest, now)
	m.operationQueueReleases(2)
	m.operationQueueReleases(0)
	if q, age, released := testutil.ToFloat64(m.queued), testutil.ToFloat64(m.queueOldest), testutil.ToFloat64(m.queueReleases); q != 3 || age != 90 || released != 2 {
		t.Fatalf("queued=%v oldest=%v released=%v", q, age, released)
	}
	m.operationQueue(0, nil, now)
	if q, age := testutil.ToFloat64(m.queued), testutil.ToFloat64(m.queueOldest); q != 0 || age != 0 {
		t.Fatalf("empty queue reported queued=%v oldest=%v", q, age)
	}
	var none *Metrics
	none.operationQueue(1, &oldest, now)
	none.operationQueueReleases(1)
}

// ADR-20261018183000: child starts count by parent and result; the fan-in
// gauge reads how long the oldest ready parent has waited, 0 when none.
func TestMetricsReportChildren(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg, "apid")
	if again := NewMetrics(reg, "apid"); again.childStarts != m.childStarts || again.fanInSignals != m.fanInSignals || again.fanInOldest != m.fanInOldest {
		t.Fatal("a second registration made second child metrics")
	}
	m.childStart("job", "started")
	m.childStart("job", "replayed")
	m.childStart("job", "started")
	m.fanInSignal("error")
	now := time.Now()
	ready := now.Add(-45 * time.Second)
	m.fanIn(&ready, now)
	if started, replayed, failed, age := testutil.ToFloat64(m.childStarts.WithLabelValues("job", "started")), testutil.ToFloat64(m.childStarts.WithLabelValues("job", "replayed")), testutil.ToFloat64(m.fanInSignals.WithLabelValues("error")), testutil.ToFloat64(m.fanInOldest); started != 2 || replayed != 1 || failed != 1 || age != 45 {
		t.Fatalf("started=%v replayed=%v error=%v oldest=%v", started, replayed, failed, age)
	}
	future := now.Add(time.Second)
	m.fanIn(&future, now)
	if age := testutil.ToFloat64(m.fanInOldest); age != 0 {
		t.Fatalf("a parent ready after the pass began reported %v", age)
	}
	m.fanIn(&ready, now)
	m.fanIn(nil, now)
	if age := testutil.ToFloat64(m.fanInOldest); age != 0 {
		t.Fatalf("no ready parent reported %v", age)
	}
	var none *Metrics
	none.childStart("http", "refused")
	none.fanInSignal("signaled")
	none.fanIn(&ready, now)
}
