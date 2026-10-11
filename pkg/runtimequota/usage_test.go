package runtimequota

// adr: 798
import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func usageFixture(g Grant) RuntimeUsage {
	minute := time.UnixMicro(g.IssuedAtMicros).UTC().Truncate(time.Minute)
	digest := sha256.Sum256([]byte("retained original resource intervals"))
	return RuntimeUsage{Grant: g, MinuteMicros: minute.UnixMicro(), MBSeconds: 520 * 17, SourceCount: 1, SourceDigest: hex.EncodeToString(digest[:]), ObservedAtMicros: minute.Add(time.Minute).UnixMicro()}
}

func TestRuntimeUsageProtocol(t *testing.T) {
	g, directory, cell, trust := fixture(t)
	u := usageFixture(g)
	e, err := cell.SignRuntimeUsage(u)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := trust.VerifyRuntimeUsage(e); err != nil || got != u {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := DecodeRuntimeUsage(raw); err != nil || got != e {
		t.Fatalf("transport: %+v %v", got, err)
	}
	if _, err := directory.SignRuntimeUsage(u); err == nil {
		t.Fatal("directory asserted cell usage")
	}
	wrong, err := NewTrust([]Anchor{{Role: Directory, IssuerID: cell.IssuerID(), Public: cell.private.Public().(ed25519.PublicKey)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrong.VerifyRuntimeUsage(e); !errors.Is(err, ErrTrust) {
		t.Fatalf("wrong role: %v", err)
	}
	retirement, err := directory.SignRetirement(Retirement{Grant: g, RetirementID: g.AllocationID, RetiredAtMicros: u.ObservedAtMicros})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := trust.VerifyRuntimeUsage(retirement); err == nil {
		t.Fatal("retirement became usage")
	}
	for _, raw := range [][]byte{append([]byte(" "), raw...), []byte(strings.Repeat("a", api.GlobalRuntimeUsageEnvelopeMaxBytes+1))} {
		if _, err := DecodeRuntimeUsage(raw); err == nil {
			t.Fatal("ambiguous/oversized transport accepted")
		}
	}
	// Historical observations are matched to retained issuance, rather than
	// being reinterpreted as a newly issued grant under today's plan table.
	u.Grant.Policy.Concurrency++
	u.Grant.Policy.PeakConcurrency++
	u.Grant.Policy.ID = api.RuntimeQuotaPolicyID(u.Grant.Policy.Plan, u.Grant.Policy.Concurrency, u.Grant.Policy.PeakConcurrency)
	if u.Grant.Validate() == nil {
		t.Fatal("historical policy is current")
	}
	e, err = cell.SignRuntimeUsage(u)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := trust.VerifyRuntimeUsage(e); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeUsageInvalidClaims(t *testing.T) {
	g, _, cell, trust := fixture(t)
	u := usageFixture(g)
	for name, mutate := range map[string]func(*RuntimeUsage){
		"partial minute":  func(u *RuntimeUsage) { u.MinuteMicros++ },
		"before issuance": func(u *RuntimeUsage) { u.MinuteMicros -= time.Minute.Microseconds() },
		"open minute":     func(u *RuntimeUsage) { u.ObservedAtMicros = u.MinuteMicros + 1 },
		"minute end overflow": func(u *RuntimeUsage) {
			u.MinuteMicros = math.MaxInt64 / time.Minute.Microseconds() * time.Minute.Microseconds()
		},
		"negative compute":  func(u *RuntimeUsage) { u.MBSeconds = -1 },
		"quantity overflow": func(u *RuntimeUsage) { u.MBSeconds = api.GlobalRuntimeUsageQuantityMax + 1 },
		"missing source":    func(u *RuntimeUsage) { u.SourceCount = 0 },
		"spurious source":   func(u *RuntimeUsage) { u.MBSeconds = 0 },
		"invalid digest":    func(u *RuntimeUsage) { u.SourceDigest = "not-a-digest" },
		"uppercase digest":  func(u *RuntimeUsage) { u.SourceDigest = strings.ToUpper(u.SourceDigest) },
		"invalid owner":     func(u *RuntimeUsage) { u.Grant.OwnerID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := u
			mutate(&bad)
			if _, err := cell.SignRuntimeUsage(bad); err == nil {
				t.Fatal("invalid observation signed")
			}
			forged, err := signBounded(cell, "runtime-usage", bad, api.GlobalRuntimeUsagePayloadMaxBytes)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := trust.VerifyRuntimeUsage(forged); err == nil {
				t.Fatal("signed invalid observation accepted")
			}
		})
	}
	e, err := cell.SignRuntimeUsage(u)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := base64.RawURLEncoding.DecodeString(e.Payload)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{" " + string(payload), strings.Replace(string(payload), `"MBSeconds":8840`, `"MBSeconds":8840,"MBSeconds":8840`, 1), strings.Replace(string(payload), `"MBSeconds":8840`, `"MBSeconds":8.84e3`, 1)} {
		bad := Envelope{KeyID: e.KeyID, Payload: base64.RawURLEncoding.EncodeToString([]byte(raw)), Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(cell.private, signedBytes("runtime-usage", e.KeyID, []byte(raw))))}
		if _, err := trust.VerifyRuntimeUsage(bad); err == nil {
			t.Fatal("noncanonical signed claims accepted")
		}
	}
	u.MBSeconds = 0
	u.SourceCount = 0
	if _, err := cell.SignRuntimeUsage(u); err != nil {
		t.Fatalf("explicit zero coverage: %v", err)
	}
}
