package clientlimit

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"path"
	"runtime/metrics"
	"strconv"
	"strings"
	"time"
)

// schedLatencies is the runtime metric of how long goroutines waited to run.
const schedLatencies = "/sched/latencies:seconds"

// NodeSampler reads the overload signals (ADR-20261110123517 §1) from
// cumulative counters, each over the last Window: the edge's own goroutine
// scheduling delay, the node's CPU pressure (PressurePath, the "some total"
// line of /proc/pressure/cpu) and the gateways' CPU use (the usage_usec of
// each cpu.stat in CgroupStats) as a share of CPUs. A source it cannot read
// counts as zero. It is not safe for concurrent use; Overload.Run calls it
// from one goroutine.
type NodeSampler struct {
	Window       time.Duration
	PressurePath string
	CgroupStats  []string
	CPUs         int
	// ReadFile and Sched default to os.ReadFile and runtime/metrics.
	ReadFile func(string) ([]byte, error)
	Sched    func() *metrics.Float64Histogram

	ring []nodeReading
}

type nodeReading struct {
	at                  time.Time
	pressure, usage     uint64
	pressureOK, usageOK bool
	schedCounts         []uint64
	schedBuckets        []float64
}

// Sample reads the counters at now and returns the signals over the window
// that ends there. The first call has nothing to compare and returns zeros.
func (s *NodeSampler) Sample(now time.Time) (OverloadSignals, error) {
	cur, err := s.read(now)
	s.ring = append(s.ring, cur)
	// Keep as the base the newest reading at least Window old.
	for len(s.ring) > 2 && now.Sub(s.ring[1].at) >= s.Window {
		s.ring = s.ring[1:]
	}
	base := s.ring[0]
	dt := cur.at.Sub(base.at)
	if dt <= 0 {
		return OverloadSignals{}, err
	}
	var out OverloadSignals
	us := float64(dt.Microseconds())
	if cur.pressureOK && base.pressureOK && cur.pressure >= base.pressure {
		out.NodeCPUPressure = min(1, float64(cur.pressure-base.pressure)/us)
	}
	if cur.usageOK && base.usageOK && cur.usage >= base.usage {
		out.GatewayCPUShare = min(1, float64(cur.usage-base.usage)/(us*float64(max(s.CPUs, 1))))
	}
	out.SchedDelayP99 = histogramQuantile(base.schedCounts, cur.schedCounts, cur.schedBuckets, 0.99)
	return out, err
}

func (s *NodeSampler) read(now time.Time) (nodeReading, error) {
	r := nodeReading{at: now}
	readFile := s.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}
	var errs []error
	if s.PressurePath != "" {
		b, err := readFile(s.PressurePath)
		if err == nil {
			r.pressure, err = psiSomeTotal(b)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("cpu pressure %s: %w", s.PressurePath, err))
		} else {
			r.pressureOK = true
		}
	}
	if len(s.CgroupStats) > 0 {
		r.usageOK = true
		for _, p := range s.CgroupStats {
			b, err := readFile(p)
			var v uint64
			if err == nil {
				v, err = cpuStatUsage(b)
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("cgroup cpu %s: %w", p, err))
				r.usageOK = false
				continue
			}
			r.usage += v
		}
	}
	h := s.sched()
	if h != nil {
		r.schedCounts = append([]uint64(nil), h.Counts...)
		r.schedBuckets = h.Buckets
	}
	if len(errs) > 0 {
		return r, fmt.Errorf("overload signals: %w", errors.Join(errs...))
	}
	return r, nil
}

func (s *NodeSampler) sched() *metrics.Float64Histogram {
	if s.Sched != nil {
		return s.Sched()
	}
	sample := []metrics.Sample{{Name: schedLatencies}}
	metrics.Read(sample)
	if sample[0].Value.Kind() != metrics.KindFloat64Histogram {
		return nil
	}
	return sample[0].Value.Float64Histogram()
}

// psiSomeTotal reads the total, in microseconds, of a pressure file's "some"
// line.
func psiSomeTotal(b []byte) (uint64, error) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 || fields[0] != "some" {
			continue
		}
		for _, f := range fields[1:] {
			if v, ok := strings.CutPrefix(f, "total="); ok {
				return strconv.ParseUint(v, 10, 64)
			}
		}
	}
	return 0, errors.New(`no "some ... total=" line`)
}

// cpuStatUsage reads usage_usec from a cgroup's cpu.stat.
func cpuStatUsage(b []byte) (uint64, error) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "usage_usec "); ok {
			return strconv.ParseUint(strings.TrimSpace(v), 10, 64)
		}
	}
	return 0, errors.New("no usage_usec line")
}

// histogramQuantile is the q quantile of the observations a cumulative
// histogram gained between base and cur, as the upper bound of the bucket it
// falls in (the lower bound for the last, unbounded bucket).
func histogramQuantile(base, cur []uint64, buckets []float64, q float64) time.Duration {
	if len(cur) == 0 || len(buckets) != len(cur)+1 {
		return 0
	}
	if len(base) != len(cur) {
		base = nil
	}
	deltas := make([]uint64, len(cur))
	var total uint64
	for i, c := range cur {
		d := c
		if base != nil {
			if c < base[i] {
				return 0
			}
			d = c - base[i]
		}
		deltas[i] = d
		total += d
	}
	if total == 0 {
		return 0
	}
	target := uint64(math.Ceil(q * float64(total)))
	var cum uint64
	for i, d := range deltas {
		cum += d
		if cum >= target {
			bound := buckets[i+1]
			if math.IsInf(bound, 1) {
				bound = buckets[i]
			}
			return time.Duration(bound * float64(time.Second))
		}
	}
	return 0
}

// GatewayCgroupStats returns the cpu.stat paths of this process's cgroup and
// of each sibling unit, from /proc/self/cgroup's unified ("0::") line and the
// cgroup2 mount at root.
func GatewayCgroupStats(procSelfCgroup []byte, root string, siblings ...string) ([]string, error) {
	sc := bufio.NewScanner(bytes.NewReader(procSelfCgroup))
	for sc.Scan() {
		rel, ok := strings.CutPrefix(sc.Text(), "0::")
		if !ok {
			continue
		}
		rel = strings.TrimSpace(rel)
		if rel == "" || rel == "/" {
			return nil, errors.New("process is in the root cgroup")
		}
		self := path.Join(root, rel)
		out := []string{path.Join(self, "cpu.stat")}
		for _, s := range siblings {
			out = append(out, path.Join(path.Dir(self), s, "cpu.stat"))
		}
		return out, nil
	}
	return nil, errors.New("no cgroup v2 line")
}
