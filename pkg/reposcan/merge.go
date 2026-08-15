package reposcan

import "sort"

// mergeByKey collapses workloadSeeds with the same (RootDir, Name)
// into a single Workload. Merge semantics, verbatim from impl
// plan §3.227-231:
//
//   - Identity fields (Name, RootDir, Tier, Source, Dockerfile) come
//     from the highest-priority seed. Priority is (tier desc,
//     detector.priority desc, source asc, rootDir asc) — the
//     detector tag is the load-bearing tiebreak for two seeds at
//     the same tier (e.g. compose + Procfile both naming `web`).
//   - Per-field-fillable fields (Class, Command, Schedule, Ports,
//     EnvKeys) are filled by the first non-empty under the same
//     priority order. The canonical example: a Procfile `web`
//     (TierCompose, detProcfile) fills the class field of a
//     compose `web` (TierCompose, detCompose) when compose didn't
//     set it.
//
// Determinism: maps are not safe under -race. Sort seeds by the
// priority key above before iterating so the merge is
// reproducible regardless of slice ordering.
func mergeByKey(seeds []workloadSeed) []Workload {
	sorted := make([]workloadSeed, len(seeds))
	copy(sorted, seeds)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].tier != sorted[j].tier {
			return sorted[i].tier > sorted[j].tier
		}
		if sorted[i].det.priority() != sorted[j].det.priority() {
			return sorted[i].det.priority() > sorted[j].det.priority()
		}
		if sorted[i].rootDir != sorted[j].rootDir {
			return sorted[i].rootDir < sorted[j].rootDir
		}
		return sorted[i].source < sorted[j].source
	})

	// Group seeds by (RootDir, Name) keeping first-arrival order.
	type bucket struct {
		name       string
		rootDir    string
		tier       Tier   // highest tier seen (=first arrival under the sort)
		source     string // highest-tier seed's source
		dockerfile string // highest-tier seed's dockerfile
		class      Class
		command    []string
		schedule   string
		ports      []int
		envKeys    []string
		// Whether each per-field slot is filled. We never overwrite
		// an already-filled field — first non-empty per tier order wins.
		classSet  bool
		cmdSet    bool
		schedSet  bool
		portsSet  bool
		envSet    bool
		dfSet     bool
		sourceSet bool
		// det is the detector that won identity — the same first
		// arrival that wins source/tier, so it is set under the
		// sourceSet gate below and never overwritten (issue #742).
		det detector
		// mergedFrom collects the OTHER detectors that landed in
		// this bucket, in first-arrival (= priority) order. A slice
		// rather than a map keeps the output deterministic without
		// a second sort; the cardinality is bounded by the detector
		// enum (8), so the linear contains-check is cheaper than a
		// map allocation per bucket.
		mergedFrom []string
	}
	ordered := make([]workloadKey, 0, len(sorted))
	buckets := make(map[workloadKey]*bucket, len(sorted))

	for _, s := range sorted {
		k := s.key()
		b, ok := buckets[k]
		if !ok {
			b = &bucket{name: s.name, rootDir: s.rootDir}
			buckets[k] = b
			ordered = append(ordered, k)
		}
		// Identity fields — first arrival wins (=highest tier under sort).
		if !b.sourceSet {
			b.source = s.source
			b.tier = s.tier
			b.det = s.det
			b.sourceSet = true
		} else if name := s.det.String(); name != b.det.String() && !containsString(b.mergedFrom, name) {
			// A non-winning seed collapsed into this bucket. Record
			// its detector once. The winner is excluded (it is
			// already Detection.Detector), and a second seed from
			// the SAME detector is not a merge across detectors —
			// e.g. two compose services in one file — so it is
			// deduplicated rather than repeated.
			b.mergedFrom = append(b.mergedFrom, name)
		}
		if !b.dfSet && s.dockerfile != "" {
			b.dockerfile = s.dockerfile
			b.dfSet = true
		}
		// Per-field: first non-empty wins (and never overwrites).
		if !b.classSet && s.class != "" {
			b.class = s.class
			b.classSet = true
		}
		if !b.cmdSet && len(s.command) > 0 {
			b.command = append([]string(nil), s.command...)
			b.cmdSet = true
		}
		if !b.schedSet && s.schedule != "" {
			b.schedule = s.schedule
			b.schedSet = true
		}
		if !b.portsSet && len(s.ports) > 0 {
			b.ports = append([]int(nil), s.ports...)
			b.portsSet = true
		}
		if !b.envSet && len(s.envKeys) > 0 {
			b.envKeys = append([]string(nil), s.envKeys...)
			b.envSet = true
		}
	}

	out := make([]Workload, 0, len(ordered))
	for _, k := range ordered {
		b := buckets[k]
		cls := b.class
		if cls == "" {
			cls = ClassUnknown
		}
		out = append(out, Workload{
			Name:       b.name,
			RootDir:    b.rootDir,
			Dockerfile: b.dockerfile,
			Command:    b.command,
			Class:      cls,
			Schedule:   b.schedule,
			Ports:      b.ports,
			EnvKeys:    b.envKeys,
			Source:     b.source,
			Tier:       b.tier,
			DetectedBy: Detection{
				Detector:   b.det.String(),
				Priority:   b.det.priority(),
				MergedFrom: b.mergedFrom,
			},
		})
	}
	return out
}

// containsString is the linear membership check used by the
// mergedFrom accumulator. The slice is bounded by the detector enum
// (8 values), so this beats a per-bucket map allocation.
func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
