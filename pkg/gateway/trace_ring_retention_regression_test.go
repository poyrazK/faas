// adr: 431
package gateway_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/gateway"
)

// This uses only the pre-fix public API so the regression can run unchanged
// against the base revision, where count-only retention accepts every tree.
func TestTraceRingDefaultBudgetEvictsLargeTreesBeforeCountCap(t *testing.T) {
	ring := gateway.NewTraceRing(gateway.DefaultTraceRingCap)
	const requests = 10_000
	for i := 0; i < requests; i++ {
		ring.Add(&gateway.Trace{TraceID: fmt.Sprint(i), Spans: []gateway.SpanRow{{
			SpanID: "one", Attributes: map[string]string{"url": strings.Repeat("x", 8192)},
		}}})
	}
	if ring.Len() == requests {
		t.Fatal("retained every large tree: entry count alone does not bound trace memory")
	}
	if _, ok := ring.Get(fmt.Sprint(requests - 1)); !ok {
		t.Fatal("newest fitting trace must remain available")
	}
}

func TestTraceRingAttributeOwnershipRegression(t *testing.T) {
	ring := gateway.NewTraceRing(10)
	attrs := map[string]string{"url": "original"}
	ring.Add(&gateway.Trace{TraceID: "one", Spans: []gateway.SpanRow{{SpanID: "one", Attributes: attrs}}})
	attrs["url"] = "changed outside the ring"
	got, _ := ring.Get("one")
	if got.Spans[0].Attributes["url"] != "original" {
		t.Fatal("input map mutation changed retained data outside admission accounting")
	}
	got.Spans[0].Attributes["url"] = "changed through Get"
	again, _ := ring.Get("one")
	if again.Spans[0].Attributes["url"] != "original" {
		t.Fatal("output map mutation changed retained data outside admission accounting")
	}
}
