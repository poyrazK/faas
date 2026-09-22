// spec: §6.2
// adr: 194

package targets

import (
	"math"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched/scalesignal"
)

// Property tests for the SCALING DECISION layer.
//
// Gap #4 of the scaling test audit: the existing property tests
// (invariants_property_test.go, ledger_property_test.go) pin §6.2-1 at
// ADMISSION — the ledger caps per-app concurrency however many instances a
// trigger asks for. That is the real safety net and it is well covered.
//
// Nothing pinned the layer that decides HOW MANY to ask for. Six signals now
// feed one arbiter, and the properties below are the ones a reader of
// decideTargets would assume and that no example-based test establishes
// across arbitrary inputs: the cap holds, a hot signal always makes
// progress, the arithmetic is self-consistent, and — the two that actually
// constrain the multi-signal design — combining signals is monotonic and
// order-independent.
//
// Byte-decoded fuzzing matching the ledger's idiom, so a failing corpus
// entry is reproducible and minimisable by `go test -run` on the seed.

// propMetrics is the closed set, in a fixed order so a corpus entry decodes
// to the same observation across runs.
var propMetrics = api.ScalingMetrics()

// decodeObservation turns three bytes into one observation. Values are kept
// small so arbitrary combinations stay in a range where the arithmetic is
// meaningful rather than saturating: a target of 0 disables the signal,
// which the arbiter must treat as "not declared" rather than dividing by it.
func decodeObservation(b0, b1, b2 byte) scalesignal.Observation {
	return scalesignal.Observation{
		Metric:   propMetrics[int(b0)%len(propMetrics)],
		Target:   float64(b1%50) + 1, // 1..50, never zero
		Measured: float64(b2),        // 0..255
		Have:     b0&0x80 == 0,       // ~half the time there is no reading
	}
}

// decodeLimits turns two bytes into the per-app envelope.
func decodeLimits(b0, b1 byte) limits {
	maxConc := int(b0%20) + 1 // 1..20, the plan cap range
	conc := int(b1) % (maxConc + 1)
	return limits{
		MaxConcurrency: maxConc,
		Concurrency:    conc,
		Now:            time.Unix(1_700_000_000, 0),
		// Cooldown deliberately disabled: these properties are about the
		// capacity arithmetic, and a cooldown short-circuit would mask
		// most inputs behind an early return.
	}
}

// FuzzDecideTargets_CapacityInvariants pins the four properties that hold
// for ANY combination of declared signals and readings.
func FuzzDecideTargets_CapacityInvariants(f *testing.F) {
	// Seeds: one signal at target, one far over, two signals disagreeing,
	// and a fleet already at its cap.
	f.Add([]byte{0x02, 0x09, 0x0a, 0x05, 0x00})
	f.Add([]byte{0x03, 0x01, 0xff, 0x14, 0x01})
	f.Add([]byte{0x00, 0x05, 0x64, 0x02, 0x32, 0x0a, 0x03, 0x01})
	f.Add(make([]byte, 16))

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 5 {
			return
		}
		lim := decodeLimits(data[0], data[1])
		var obs []scalesignal.Observation
		for i := 2; i+2 < len(data) && len(obs) < api.MaxScalingTargets; i += 3 {
			obs = append(obs, decodeObservation(data[i], data[i+1], data[i+2]))
		}
		if len(obs) == 0 {
			return
		}
		dec := decideTargets(lim, obs)

		// 1. The plan cap is never exceeded. The ledger would refuse
		//    anyway, but a decision that asks for more than the cap
		//    burns an admission attempt and an audit row on every tick.
		if dec.Desired > lim.MaxConcurrency {
			t.Fatalf("desired %d exceeds MaxConcurrency %d (obs=%+v)",
				dec.Desired, lim.MaxConcurrency, obs)
		}

		if !dec.ShouldAdmit {
			// 2. A decision that does not admit must not claim
			//    admissions; the caller loops over them.
			if dec.Admissions != 0 {
				t.Fatalf("outcome=%s but Admissions=%d", dec.Outcome, dec.Admissions)
			}
			return
		}

		// 3. Admitting always makes progress. A "hot" decision that
		//    asks for the instances it already has would leave the app
		//    saturated forever while reporting success every tick.
		if dec.Desired <= lim.Concurrency {
			t.Fatalf("admitting but desired %d <= concurrency %d (obs=%+v)",
				dec.Desired, lim.Concurrency, obs)
		}

		// 4. The arithmetic is self-consistent: the caller admits
		//    exactly the gap it is told about.
		if want := dec.Desired - lim.Concurrency; dec.Admissions != want {
			t.Fatalf("Admissions=%d, want Desired-Concurrency=%d", dec.Admissions, want)
		}
	})
}

// FuzzArbitrate_AddingASignalNeverLowersDesired pins the MAX semantics that
// the whole multi-signal design rests on.
//
// ADR-194's contract is that a developer lists signals and the platform
// provisions for the most demanding one. If adding a signal could lower the
// result, declaring an extra target could silently shrink a fleet — the
// opposite of what the feature promises, and a failure mode a developer
// would have no way to anticipate.
func FuzzArbitrate_AddingASignalNeverLowersDesired(f *testing.F) {
	f.Add([]byte{0x05, 0x02, 0x09, 0x0a, 0x01, 0x05, 0x50})
	f.Add([]byte{0x01, 0x00, 0x01, 0xff, 0x03, 0x01, 0x02})
	f.Add(make([]byte, 8))

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 7 {
			return
		}
		conc := int(data[0]) % 20
		base := []scalesignal.Observation{decodeObservation(data[1], data[2], data[3])}
		extra := decodeObservation(data[4], data[5], data[6])

		before := scalesignal.Arbitrate(conc, base)
		after := scalesignal.Arbitrate(conc, append(append([]scalesignal.Observation{}, base...), extra))

		if before.Hot && !after.Hot {
			t.Fatalf("adding a signal turned a hot decision cold: base=%+v extra=%+v", base, extra)
		}
		if before.Hot && after.Desired < before.Desired {
			t.Fatalf("adding a signal LOWERED desired from %d to %d: base=%+v extra=%+v",
				before.Desired, after.Desired, base, extra)
		}
	})
}

// FuzzArbitrate_OrderIndependent pins that the declared order of targets
// carries no meaning.
//
// A customer reordering a YAML list must not change how their app scales.
// This is also what lets the validator reject duplicate metrics without
// having to define a precedence between them.
func FuzzArbitrate_OrderIndependent(f *testing.F) {
	f.Add([]byte{0x03, 0x00, 0x05, 0x64, 0x02, 0x0a, 0x1e, 0x01, 0x03, 0x09})
	f.Add(make([]byte, 10))

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 10 {
			return
		}
		conc := int(data[0]) % 20
		obs := []scalesignal.Observation{
			decodeObservation(data[1], data[2], data[3]),
			decodeObservation(data[4], data[5], data[6]),
			decodeObservation(data[7], data[8], data[9]),
		}
		forward := scalesignal.Arbitrate(conc, obs)
		reversed := scalesignal.Arbitrate(conc, []scalesignal.Observation{obs[2], obs[1], obs[0]})

		if forward.Hot != reversed.Hot || forward.Desired != reversed.Desired {
			t.Fatalf("order changed the decision: forward=%+v reversed=%+v obs=%+v",
				forward, reversed, obs)
		}
		// The WINNER may legitimately differ when two signals tie on
		// desired, so it is not asserted here — only the capacity is a
		// contract. Asserting the winner would make this test fail on a
		// tie, which is a real and harmless input.
	})
}

// FuzzArbitrate_NeverProducesNonsense is the defensive property: whatever a
// stored policy or a misbehaving source hands the arbiter, the result must
// be a usable instance count.
//
// Rows written before a metric was retired, a pusher sending a value the
// validator would now reject, or a reader returning a garbage sample all
// reach this function in production.
func FuzzArbitrate_NeverProducesNonsense(f *testing.F) {
	f.Add([]byte{0x02, 0x01, 0x05, 0xff})
	f.Add(make([]byte, 4))

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 4 {
			return
		}
		conc := int(data[0]) % 20
		obs := []scalesignal.Observation{decodeObservation(data[1], data[2], data[3])}
		// Also exercise inputs the validator rejects but a stored row
		// could still carry.
		obs = append(obs,
			scalesignal.Observation{Metric: "retired_metric", Target: 5, Measured: 100, Have: true},
			scalesignal.Observation{Metric: api.ScalingMetricCPU, Target: 0, Measured: 90, Have: true},
		)

		res := scalesignal.Arbitrate(conc, obs)
		if res.Hot {
			if res.Desired <= conc {
				t.Fatalf("hot but desired %d <= concurrency %d", res.Desired, conc)
			}
			if res.Winner == "" {
				t.Fatal("hot decision with no winning metric: the admission would be unattributable")
			}
			if _, known := scalesignal.ClassOf(res.Winner); !known {
				t.Fatalf("winner %q is not a classified metric", res.Winner)
			}
		} else if res.Desired != 0 {
			t.Fatalf("not hot but desired=%d, want 0", res.Desired)
		}
		if res.Desired < 0 || res.Desired > math.MaxInt32 {
			t.Fatalf("desired %d is not a usable instance count", res.Desired)
		}
	})
}
