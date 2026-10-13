package runtimequota

// adr: 802
import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestUsageImportAckProtocol(t *testing.T) {
	g, directory, cell, trust := fixture(t)
	u := usageFixture(g)
	a := UsageImportAck{Usage: u, ImportedAtMicros: u.ObservedAtMicros + 1}
	e, err := directory.SignUsageImportAck(a)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := trust.VerifyUsageImportAck(e); err != nil || got != a {
		t.Fatal(got, err)
	}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := DecodeUsageImportAck(raw); err != nil || got != e {
		t.Fatal(got, err)
	}
	if _, err := cell.SignUsageImportAck(a); err == nil {
		t.Fatal("cell acknowledged directory import")
	}
	usage, err := cell.SignRuntimeUsage(u)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := trust.VerifyUsageImportAck(usage); err == nil {
		t.Fatal("usage signature became import receipt")
	}
	if _, err := trust.VerifyRuntimeUsage(e); err == nil {
		t.Fatal("receipt became usage")
	}
	a.ImportedAtMicros = u.ObservedAtMicros - 1
	if _, err := directory.SignUsageImportAck(a); err != nil {
		t.Fatal("database clock skew rejected committed import", err)
	}
	for name, mutate := range map[string]func(*UsageImportAck){
		"zero-import-time": func(a *UsageImportAck) { a.ImportedAtMicros = 0 },
		"overflow":         func(a *UsageImportAck) { a.ImportedAtMicros = api.GlobalRuntimeQuotaGenerationMax + 1 },
		"invalid-usage":    func(a *UsageImportAck) { a.Usage.SourceCount = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			bad := a
			mutate(&bad)
			if _, err := directory.SignUsageImportAck(bad); !errors.Is(err, ErrInvalid) {
				t.Fatal(err)
			}
		})
	}
	for _, bad := range [][]byte{append([]byte(" "), raw...), []byte(strings.Repeat("x", api.GlobalRuntimeUsageAckEnvelopeMaxBytes+1))} {
		if _, err := DecodeUsageImportAck(bad); err == nil {
			t.Fatal("ambiguous/oversized receipt decoded")
		}
	}
}
