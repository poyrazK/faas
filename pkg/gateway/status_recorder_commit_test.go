// spec: §4.1
package gateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStatusRecorderImplicitResponseAppliesHeaderRules(t *testing.T) {
	for _, mode := range []string{"body", "stream flush", "grpc flush"} {
		t.Run(mode, func(t *testing.T) {
			live := httptest.NewRecorder()
			rec := newTestStatusRecorder(live)
			rec.installHeaderOps([]EdgeRuleHeaderOp{
				{Action: "set", Name: "Access-Control-Allow-Origin", Value: "https://client.example"},
				{Action: "add", Name: "X-Edge-Rule", Value: "applied"},
				{Action: "remove", Name: "X-Origin-Private"},
			})
			rec.Header().Set("Content-Type", "application/grpc")
			rec.Header().Set("X-Origin-Private", "internal")
			rec.Header().Set(preAuthTargetHeader, "private-target")
			if mode == "stream flush" {
				rec.installFlushHook(live, nil, 1<<20, time.Hour, 0)
			}
			if mode != "body" {
				rec.Flush()
			}
			if _, err := rec.Write([]byte("first")); err != nil {
				t.Fatal(err)
			}
			if _, err := rec.Write([]byte("second")); err != nil {
				t.Fatal(err)
			}
			response := live.Result()
			defer response.Body.Close()
			if response.Header.Get("Access-Control-Allow-Origin") != "https://client.example" || len(response.Header.Values("X-Edge-Rule")) != 1 {
				t.Errorf("edge rules were not applied exactly once before commit: %v", response.Header)
			}
			if response.Header.Get("X-Origin-Private") != "" || response.Header.Get(preAuthTargetHeader) != "" {
				t.Errorf("private response headers reached the client: %v", response.Header)
			}
			if rec.ContentType != "application/grpc" || rec.preAuthTarget != "private-target" || rec.preAuthTargetCount != 1 {
				t.Errorf("response metadata was not captured at commit: contentType=%q target=%q count=%d", rec.ContentType, rec.preAuthTarget, rec.preAuthTargetCount)
			}
		})
	}
}

// adr: 1505 — an implicit HEAD response completes at its first flush, so
// durable usage must precede that flush just as it does an explicit status.
func TestStatusRecorderImplicitFlushRequiresDurableUsage(t *testing.T) {
	for _, maxBytes := range []int64{1, 4096} {
		for _, streaming := range []bool{false, true} {
			f := newUsageCommitFixture(t, http.MethodHead, maxBytes, true)
			f.rec.Header().Set("Content-Length", "4")
			f.rec.Header().Set("Content-Type", "application/grpc")
			if streaming {
				f.rec.installFlushHook(f.probe, nil, 1<<20, time.Hour, 0)
			}
			f.rec.Flush()
			want := int64(1)
			if maxBytes == 1 {
				want = -1 // The outbox cannot append: do not flush at all.
			}
			if f.probe.flushedAt != want {
				t.Errorf("maxBytes=%d streaming=%v: flush saw %d durable events, want %d", maxBytes, streaming, f.probe.flushedAt, want)
			}
		}
	}
}

func TestStatusRecorderImplicitWriteKeepsUndurableResponseUncommitted(t *testing.T) {
	f := newUsageCommitFixture(t, http.MethodGet, 1, true)
	f.rec.Header().Set("Content-Length", "4")
	f.rec.installHeaderOps([]EdgeRuleHeaderOp{{Action: "set", Name: "X-Edge-Rule", Value: "applied"}})
	if n, err := f.rec.Write([]byte("body")); n != 0 || !errors.Is(err, errUsageNotDurable) {
		t.Fatalf("write = %d, %v, want withheld response", n, err)
	}
	if f.probe.status != 0 || len(f.probe.body) != 0 {
		t.Fatal("preparing implicit response headers committed an undurable response")
	}
}
