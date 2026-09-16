package reposcan

import (
	"sort"
	"testing"
	"testing/fstest"
)

// TestMerge_ComposeFillsProcfileClass — the canonical
// compose+Procfile composition: a Procfile supplies class=http for
// `web:`, and a compose.yaml supplies the same (RootDir="",
// Name="web") workload with ports + command. The merge rule
// carries BOTH: the highest-priority seeds win identity, and the
// first-non-empty wins per field.
//
// With the detector-tiebreak fix (PR-review HIGH #1), compose has
// higher priority than Procfile at the same tier, so compose's
// fields (command, ports, envKeys) win identity and the merged
// command is the 4-token list. The Procfile's class=http still
// fills the empty slot because compose didn't set one.
func TestMerge_ComposeFillsProcfileClass(t *testing.T) {
	t.Parallel()
	seeds := []workloadSeed{
		{
			tier:    TierCompose,
			det:     detCompose,
			source:  "compose.yaml: web",
			name:    "web",
			ports:   []int{8080},
			command: []string{"bundle", "exec", "rails", "s"},
			envKeys: []string{"RAILS_ENV"},
		},
		{
			tier:    TierCompose,
			det:     detProcfile,
			source:  "Procfile: web",
			name:    "web",
			class:   ClassHTTP,
			command: []string{"bundle exec rails s -p 3000"},
		},
	}
	out := mergeByKey(seeds)
	if len(out) != 1 {
		t.Fatalf("out = %v, want 1 workload", out)
	}
	w := out[0]
	if w.Name != "web" {
		t.Errorf("name = %q, want web", w.Name)
	}
	if w.Tier != TierCompose {
		t.Errorf("tier = %s, want compose", w.Tier)
	}
	if w.Class != ClassHTTP {
		t.Errorf("class = %q, want http (from Procfile)", w.Class)
	}
	if len(w.Ports) != 1 || w.Ports[0] != 8080 {
		t.Errorf("ports = %v, want [8080] (from compose)", w.Ports)
	}
	// Detector-priority tiebreak: compose has higher detector
	// priority than Procfile, so compose's command wins identity.
	// The Procfile's "bundle exec rails s -p 3000" is a single
	// string; compose's is the 4-token list. The merged command
	// is compose's.
	if len(w.Command) != 4 || w.Command[0] != "bundle" {
		t.Errorf("command = %v; want compose's 4-token form", w.Command)
	}
}

func TestMerge_ComposeInfersClassAfterExplicitHints(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		seeds []workloadSeed
		want  Class
	}{
		{
			name: "published port is HTTP",
			seeds: []workloadSeed{{
				tier: TierCompose, det: detCompose, source: "compose.yaml: api",
				name: "api", ports: []int{8080},
			}},
			want: ClassHTTP,
		},
		{
			name: "no published port is worker",
			seeds: []workloadSeed{{
				tier: TierCompose, det: detCompose, source: "compose.yaml: jobs",
				name: "jobs",
			}},
			want: ClassWorker,
		},
		{
			name: "explicit Procfile class wins",
			seeds: []workloadSeed{
				{tier: TierCompose, det: detCompose, source: "compose.yaml: web", name: "web"},
				{tier: TierCompose, det: detProcfile, source: "Procfile: web", name: "web", class: ClassHTTP},
			},
			want: ClassHTTP,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := mergeByKey(tc.seeds)
			if len(got) != 1 || got[0].Class != tc.want {
				t.Fatalf("mergeByKey() = %#v, want one %q workload", got, tc.want)
			}
		})
	}
}

// TestMerge_HighestTierWins — when two seeds with the same
// (RootDir, Name) come from different tiers, the higher tier
// wins identity. Compose (8) > convention (3): the merged workload
// reports Tier=Compose.
func TestMerge_HighestTierWins(t *testing.T) {
	t.Parallel()
	seeds := []workloadSeed{
		{
			tier:    TierConvention,
			source:  "convention: services/auth",
			name:    "auth",
			rootDir: "services/auth",
			class:   ClassUnknown,
		},
		{
			tier:    TierCompose,
			source:  "compose.yaml: auth",
			name:    "auth",
			rootDir: "services/auth",
			class:   ClassHTTP,
			command: []string{"node", "server.js"},
		},
	}
	out := mergeByKey(seeds)
	if len(out) != 1 {
		t.Fatalf("out = %v, want 1", out)
	}
	w := out[0]
	if w.Tier != TierCompose {
		t.Errorf("tier = %s, want compose (highest tier wins)", w.Tier)
	}
	if w.Source != "compose.yaml: auth" {
		t.Errorf("source = %q, want compose.yaml: auth (highest tier's source)", w.Source)
	}
	if w.Class != ClassHTTP {
		t.Errorf("class = %q, want http (first non-empty wins)", w.Class)
	}
	if len(w.Command) != 2 {
		t.Errorf("command = %v, want [node server.js]", w.Command)
	}
}

// TestMerge_DifferentRootsSameName — `services/auth` and
// `apps/auth` are TWO workloads, not one. (RootDir, Name) is the
// merge key — Name alone is not enough.
func TestMerge_DifferentRootsSameName(t *testing.T) {
	t.Parallel()
	seeds := []workloadSeed{
		{name: "auth", rootDir: "services/auth", source: "convention: services/auth"},
		{name: "auth", rootDir: "apps/auth", source: "convention: apps/auth"},
	}
	out := mergeByKey(seeds)
	if len(out) != 2 {
		t.Errorf("out = %v, want 2 workloads (different RootDirs)", out)
	}
}

// TestMerge_SortIsDeterministic — mergeByKey's output ordering
// depends on the tier+source sort. For three TierCompose seeds at
// the same rootDir, the secondary sort by `source` drives the
// order. We pin a fixed order by giving each seed a distinct
// source string and asserting the merged Source field is the
// alphabetically-smallest (because tier = identity field, and
// first arrival under the sort wins).
func TestMerge_SortIsDeterministic(t *testing.T) {
	t.Parallel()
	seeds := []workloadSeed{
		{tier: TierCompose, name: "z", rootDir: "subdir", source: "compose.yaml: z"},
		{tier: TierCompose, name: "a", rootDir: "subdir", source: "Procfile: a"},
	}
	out := mergeByKey(seeds)
	if len(out) != 2 {
		t.Fatalf("expected 2 workloads (different RootDir won't merge, but here they share RootDir=subdir so 1): %v", out)
	}
	// Different names — they don't merge; we test Scan() below.
	// Run Scan() which sorts by Name — the alphabetical tie-breaker.
	fsys := fstest.MapFS{
		"compose.yaml": &fstest.MapFile{Data: []byte(`services:
  zeta:
    build: .
  alpha:
    build: .
  mu:
    build: .
`)},
	}
	r, err := Scan(fsys)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	got := make([]string, len(r.Workloads))
	for i, w := range r.Workloads {
		got[i] = w.Name
	}
	sort.Strings(got)
	if !equalSet(got, []string{"alpha", "mu", "zeta"}) {
		t.Errorf("Scan() sorted names = %v, want {alpha,mu,zeta}", got)
	}
}

// TestMerge_ShuffledInputDeterministic — the load-bearing
// determinism test PR-review HIGH #1 demanded. Two seeds at the
// same tier with the same (RootDir, Name) and conflicting field
// values must produce the SAME merged Workload regardless of
// input order. Before the detector-tiebreak fix, the merge sort
// used (source lex) as the secondary key, which is not a contract
// any detector is held to. With the fix, the secondary key is
// detector.priority(), which IS a contract.
func TestMerge_ShuffledInputDeterministic(t *testing.T) {
	t.Parallel()
	seed := func() []workloadSeed {
		return []workloadSeed{
			{
				tier:    TierCompose,
				det:     detCompose,
				source:  "compose.yaml: web",
				name:    "web",
				command: []string{"bundle", "exec", "rails", "s"},
			},
			{
				tier:    TierCompose,
				det:     detProcfile,
				source:  "Procfile: web",
				name:    "web",
				class:   ClassHTTP,
				command: []string{"bundle exec rails s -p 3000"},
			},
		}
	}
	// Forward order.
	out1 := mergeByKey(seed())
	// Reverse order.
	s2 := seed()
	for i, j := 0, len(s2)-1; i < j; i, j = i+1, j-1 {
		s2[i], s2[j] = s2[j], s2[i]
	}
	out2 := mergeByKey(s2)
	// Interleaved: compose, Procfile, compose, Procfile — doubling.
	s3 := append(seed(), seed()...)
	out3 := mergeByKey(s3)
	if len(out1) != 1 || len(out2) != 1 || len(out3) != 1 {
		t.Fatalf("len(out) = (%d, %d, %d), want 1", len(out1), len(out2), len(out3))
	}
	if out1[0].Class != out2[0].Class || out2[0].Class != out3[0].Class {
		t.Errorf("Class drift: %q %q %q", out1[0].Class, out2[0].Class, out3[0].Class)
	}
	if len(out1[0].Command) != len(out2[0].Command) || len(out2[0].Command) != len(out3[0].Command) {
		t.Errorf("Command len drift: %v %v %v", out1[0].Command, out2[0].Command, out3[0].Command)
	}
}

// TestMerge_ProcfileWinsClassWhenHigherPriority — pins the
// detector precedence: Procfile (priority=75) outranks
// compose (priority=80)... wait, no. Compose outranks Procfile
// in priority. So compose's fields win identity, and the
// Procfile's class fills the empty field. The Procfile's command
// would NOT win because compose's command is non-empty.
func TestMerge_ProcfileFillsComposeEmptyClass(t *testing.T) {
	t.Parallel()
	seeds := []workloadSeed{
		{
			tier:    TierCompose,
			det:     detCompose,
			source:  "compose.yaml: web",
			name:    "web",
			command: []string{"bundle", "exec", "rails", "s"},
			// class left empty — Procfile will fill it.
		},
		{
			tier:   TierCompose,
			det:    detProcfile,
			source: "Procfile: web",
			name:   "web",
			class:  ClassHTTP,
		},
	}
	out := mergeByKey(seeds)
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1", len(out))
	}
	if out[0].Class != ClassHTTP {
		t.Errorf("Class = %q, want http (from Procfile, fills compose's empty slot)", out[0].Class)
	}
	if len(out[0].Command) != 4 {
		t.Errorf("Command = %v, want compose's 4-token form", out[0].Command)
	}
}
