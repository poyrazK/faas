package flags

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
	"math/rand/v2"
	"slices"
)

// Bandits (ADR 20261119173702 §4): Thompson sampling over each arm's
// Beta(1 + converted, 1 + exposed - converted) posterior.

// Experiment allocations.
const (
	ExperimentAllocationFixed  = "fixed"
	ExperimentAllocationBandit = "bandit"
)

// BanditArm is one arm's posterior summary.
type BanditArm struct {
	Variant         string  `json:"variant"`
	BestProbability float64 `json:"best_probability"`
	// ExpectedLoss is the expected conversion rate given up by choosing
	// this arm: E[max_j p_j - p_this].
	ExpectedLoss float64 `json:"expected_loss"`
}

// BanditRNG is a deterministic generator for one experiment and pass, so a
// pass can be replayed.
func BanditRNG(experimentID string, pass int64) *rand.Rand {
	h := sha256.Sum256([]byte(experimentID))
	return rand.New(rand.NewPCG(binary.BigEndian.Uint64(h[:8]), uint64(pass)))
}

// BanditPosterior draws samples from every arm's posterior and returns, in
// the arms' order sorted by variant, each arm's best-arm probability and
// expected loss.
func BanditPosterior(arms []Arm, samples int, rng *rand.Rand) []BanditArm {
	sorted := slices.Clone(arms)
	slices.SortFunc(sorted, func(a, b Arm) int { return compareText(a.Variant, b.Variant) })
	out := make([]BanditArm, len(sorted))
	if len(sorted) == 0 || samples <= 0 {
		return out
	}
	wins := make([]int, len(sorted))
	loss := make([]float64, len(sorted))
	draw := make([]float64, len(sorted))
	for s := 0; s < samples; s++ {
		best := 0
		for i, a := range sorted {
			conv := float64(max(a.Converted, 0))
			fail := float64(max(a.Exposed-a.Converted, 0))
			draw[i] = betaSample(rng, 1+conv, 1+fail)
			if draw[i] > draw[best] {
				best = i
			}
		}
		wins[best]++
		for i := range sorted {
			loss[i] += draw[best] - draw[i]
		}
	}
	for i, a := range sorted {
		out[i] = BanditArm{Variant: a.Variant, BestProbability: float64(wins[i]) / float64(samples), ExpectedLoss: loss[i] / float64(samples)}
	}
	return out
}

// BanditWeights turns best-arm probabilities into variant weights in basis
// points summing to 10000: floor + (1 - n·floor)·p, so every arm keeps at
// least floorBP. With n·floor > 10000 every arm gets an equal share.
func BanditWeights(posterior []BanditArm, floorBP int) map[string]int {
	n := len(posterior)
	out := make(map[string]int, n)
	if n == 0 {
		return out
	}
	floor := float64(floorBP) / 10000
	if float64(n)*floor > 1 {
		floor = 1 / float64(n)
	}
	shares := make([]float64, n)
	for i, a := range posterior {
		shares[i] = floor + (1-float64(n)*floor)*a.BestProbability
	}
	// Largest remainder, so the weights sum to exactly 10000.
	type rem struct {
		i int
		r float64
	}
	total := 0
	rems := make([]rem, n)
	for i, s := range shares {
		bp := int(math.Floor(s * 10000))
		out[posterior[i].Variant] = bp
		total += bp
		rems[i] = rem{i, s*10000 - float64(bp)}
	}
	slices.SortStableFunc(rems, func(a, b rem) int {
		switch {
		case a.r > b.r:
			return -1
		case a.r < b.r:
			return 1
		}
		return a.i - b.i
	})
	for k := 0; total < 10000; k++ {
		out[posterior[rems[k%n].i].Variant]++
		total++
	}
	return out
}

// BanditShift is the largest weight change between two allocations, in
// basis points.
func BanditShift(from, to map[string]int) int {
	shift := 0
	for k, w := range to {
		d := w - from[k]
		if d < 0 {
			d = -d
		}
		shift = max(shift, d)
	}
	return shift
}

// betaSample draws from Beta(a, b) as X/(X+Y) with X~Gamma(a), Y~Gamma(b).
func betaSample(rng *rand.Rand, a, b float64) float64 {
	x := gammaSample(rng, a)
	y := gammaSample(rng, b)
	if x+y == 0 {
		return 0.5
	}
	return x / (x + y)
}

// gammaSample draws from Gamma(shape, 1) (Marsaglia and Tsang 2000); shape
// is at least 1 here, since posteriors start at Beta(1, 1).
func gammaSample(rng *rand.Rand, shape float64) float64 {
	if shape < 1 {
		return gammaSample(rng, shape+1) * math.Pow(rng.Float64(), 1/shape)
	}
	d := shape - 1.0/3
	c := 1 / math.Sqrt(9*d)
	for {
		x := rng.NormFloat64()
		v := 1 + c*x
		if v <= 0 {
			continue
		}
		v = v * v * v
		u := rng.Float64()
		if u < 1-0.0331*x*x*x*x || math.Log(u) < 0.5*x*x+d*(1-v+math.Log(v)) {
			return d * v
		}
	}
}
