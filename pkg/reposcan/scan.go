package reposcan

import (
	"io/fs"
	"sort"
	"strings"
)

// Tier is the rank of the source that produced a workload. Higher
// wins in the merge rule. The values are non-contiguous (1, 3, 5, 8)
// so a future Phase-N adds a new tier without reordering existing
// ones — the gap between Convention(3) and Workspace(5) is the
// margin Phase 5's reconcile fallback can land in without churning
// prior phase output.
type Tier int

const (
	// TierSingle is the root-floor: the repo IS the app, exactly as
	// Phase 1+ shipped. Held back when any higher tier produces a
	// workload.
	TierSingle Tier = 1
	// TierConvention is the directory convention (services/*/,
	// apps/*/, packages/*/, cmd/*/) where each member carries its
	// own Dockerfile or language marker.
	TierConvention Tier = 3
	// TierWorkspace enumerates workspace-graph members (package.json
	// workspaces, pnpm-workspace.yaml, turbo.json, nx.json, go.work,
	// Cargo [workspace]) that also carry a Dockerfile/marker.
	TierWorkspace Tier = 5
	// TierCompose (and the rest of Tier 1) is the highest confidence:
	// the repo declares its workloads explicitly via compose,
	// Procfile, k8s manifests, render.yaml, fly.toml,
	// serverless.yml, or app.yaml.
	TierCompose Tier = 8
)

// Tier wire names, pinned by the OpenAPI PlanWorkload.tier enum.
//
// These are a SEPARATE vocabulary from the detector names in
// seed.go, even though `compose` appears in both: a tier is the
// confidence class of the scan, a detector is the file that produced
// the seed. They are named here so the collision is explicit — a
// future rename of one must not silently rename the other, and
// goconst cannot suggest sharing a constant across the two
// (issue #742 added the detector vocabulary and surfaced the clash).
const (
	tierNameSingle     = "single"
	tierNameConvention = "convention"
	tierNameWorkspace  = "workspace"
	tierNameCompose    = "compose"
	tierNameUnknown    = "unknown"
)

// String makes Tier printable for confirm tables and warning logs.
func (t Tier) String() string {
	switch t {
	case TierSingle:
		return tierNameSingle
	case TierConvention:
		return tierNameConvention
	case TierWorkspace:
		return tierNameWorkspace
	case TierCompose:
		return tierNameCompose
	default:
		return tierNameUnknown
	}
}

// Class is the workload-class hint produced by static scan. Phase 4
// (characterization boot, ADR-051) is the authoritative observer and
// overrides the hint; disagreements are warning + audit event. The
// schema's CHECK constraint on apps.workload_class is the same five
// canonical values plus unknown (apps/projects accept ” and
// 'http'/'graphql'/'grpc'/'job'/'worker' — unknown is the hint-only
// seventh value that gets normalized before insert).
type Class string

const (
	ClassHTTP    Class = "http"
	ClassGraphQL Class = "graphql"
	ClassGRPC    Class = "grpc"
	ClassJob     Class = "job"
	ClassWorker  Class = "worker"
	ClassServer  Class = "server" // k8s Deployment / fly app (class hint; Phase 4 corrects to http)
	ClassUnknown Class = "unknown"
)

// Workload is one discoverable unit of work. Stable, deterministic
// fields; the confirm table in Phase 3 reads these verbatim.
type Workload struct {
	Name       string   // service name; deterministic sort key
	RootDir    string   // build context relative to repo root; "" = root
	Dockerfile string   // explicit path if declared (relative to RootDir)
	Command    []string // start-command override (compose `command:`, Procfile rhs)
	Class      Class    // http|graphql|grpc|job|worker|server|unknown
	Schedule   string   // cron expression when declared (CronJob, render, serverless)
	Ports      []int
	EnvKeys    []string // KEYS only — never values; spec §11 forbids logging secrets
	Source     string   // "compose.yaml: api" (provenance; shown in confirm)
	Tier       Tier
	// DetectedBy is the explainability trace (issue #742). Source
	// already carries human-readable provenance ("compose.yaml: api");
	// this is the STRUCTURED form a client can branch on without
	// parsing that string. Populated by mergeByKey.
	DetectedBy Detection
}

// Detection is the structured answer to "why does this workload
// exist?" (issue #742). Before this, the detector that produced a
// seed was used purely as a merge tiebreak (merge.go) and then
// discarded — a user whose repo has a Dockerfile AND a package.json
// AND a docker-compose.yml had to read detector source to find out
// which one won.
//
// Detector/Priority describe the seed that won IDENTITY for this
// workload (name, root dir, tier, source, dockerfile). MergedFrom
// names the other detectors that contributed per-field values under
// the merge rule. Together they explain both halves of the merge:
// who won, and who else was in the room.
type Detection struct {
	// Detector is the closed-vocabulary name of the winning
	// detector (detector.String()).
	Detector string
	// Priority is the winning detector's tiebreak weight
	// (detector.priority()). Surfaced so a client can explain an
	// ordering to the user without hard-coding our precedence
	// table, and so a precedence change is visible on the wire.
	Priority uint8
	// MergedFrom names the OTHER detectors whose seeds collapsed
	// into this workload under the (RootDir, Name) merge key,
	// deduplicated and sorted for determinism. Empty when the
	// workload came from exactly one seed — the common case.
	//
	// This is the field that answers "why did my Procfile `web`
	// not get its own workload?": it merged into the compose
	// `web`, and compose won identity on priority.
	MergedFrom []string
}

// Key returns the merge key described in impl plan §3: pair
// (RootDir, Name). Two sources landing the same key collapse into
// one Workload via merge.go.
func (w Workload) Key() workloadKey {
	return workloadKey{RootDir: w.RootDir, Name: w.Name}
}

// Managed is a service that the repo declared (compose image:,
// render.yaml's pserviced service, etc) but that the platform will
// not provision. The two-drive FROM-base contract rejects stateful
// services at deploy-accept (ADR-046); we surface them as Managed so
// the customer sees them in the confirm table and reads a learnable
// env hint ("DATABASE_URL") instead of a runtime 422.
type Managed struct {
	Name    string // "db", "cache"
	Kind    string // postgres|redis|mysql|…
	EnvHint string // DATABASE_URL, REDIS_URL, …
	Source  string // "compose.yaml: services.db"
	Image   string // original image reference
}

// Result is the deterministic scan output. Sorted by Name ascending
// (case-insensitive). Phase 3's confirm table reads this verbatim,
// and Phase 5's reconcile diffs prior Result against new Result to
// emit create/update/remove actions.
type Result struct {
	Workloads []Workload
	Managed   []Managed
	Tier      Tier // highest tier that produced any workload
	Warnings  []string
}

// Scan walks an fs.FS (the extracted tarball in production,
// fstest.MapFS in tests) and returns a deterministic Result. A
// missing source file is a no-op, not an error: a tarball with only
// a Dockerfile is scannable as a Tier-4 single-unit. The only error
// path is invalid-path escape attempts, which fail closed so a
// malicious tarball can never reach the host.
//
// Detector fan-out order is intentional: tier 1 (compose, Procfile,
// k8s, render, fly, serverless, app.yaml) before tier 2/3 so the
// merge rule can prefer an explicit source on a (RootDir, Name)
// collision.
func Scan(fsys fs.FS) (Result, error) {
	var (
		seeds       []workloadSeed
		managed     []Managed
		warnings    []string
		highestTier Tier
	)

	// Tier 1 — explicit sources. Each detector is paired with its
	// detector tag so the merge rule can break ties on detector
	// precedence (compose > Procfile > k8s > render > fly >
	// serverless > app.yaml) rather than the free-form source
	// string. The Procfile's class=`http` wins over compose's
	// class=`unknown` because detProcfile.priority() > detCompose
	// .priority() at the same tier.
	results := []struct {
		tag detector
		run func(fs.FS) ([]workloadSeed, []Managed, []string, error)
	}{
		{detCompose, detectCompose},
		{detProcfile, detectProcfile},
		{detK8s, detectK8s},
		{detRender, detectRender},
		{detFly, detectFly},
		{detServerless, detectServerless},
		{detAppYaml, detectAppYaml},
	}
	for _, r := range results {
		s, m, w, err := r.run(fsys)
		if err != nil {
			return Result{}, err
		}
		for i := range s {
			s[i].tier = TierCompose
			s[i].det = r.tag
			seeds = append(seeds, s[i])
			if TierCompose > highestTier {
				highestTier = TierCompose
			}
		}
		managed = append(managed, m...)
		warnings = append(warnings, w...)
	}

	// Tier 2 — workspaces.
	wsSeeds, wsw, err := detectWorkspaces(fsys)
	if err != nil {
		return Result{}, err
	}
	warnings = append(warnings, wsw...)
	for _, s := range wsSeeds {
		s.tier = TierWorkspace
		seeds = append(seeds, s)
		if TierWorkspace > highestTier {
			highestTier = TierWorkspace
		}
	}

	// Tier 3 — directory convention.
	conSeeds, conw, err := detectConvention(fsys)
	if err != nil {
		return Result{}, err
	}
	warnings = append(warnings, conw...)
	for _, s := range conSeeds {
		s.tier = TierConvention
		seeds = append(seeds, s)
		if TierConvention > highestTier {
			highestTier = TierConvention
		}
	}

	// Tier 4 — root floor.
	if len(seeds) == 0 {
		seeds = append(seeds, workloadSeed{
			name:    keyApp,
			rootDir: "",
			tier:    TierSingle,
			source:  "root-floor",
		})
		highestTier = TierSingle
	}

	workloads := mergeByKey(seeds)
	sortStableByName(workloads)
	sortManagedByName(managed)

	if highestTier == 0 {
		highestTier = TierSingle
	}

	return Result{
		Workloads: workloads,
		Managed:   managed,
		Tier:      highestTier,
		Warnings:  warnings,
	}, nil
}

// sortStableByName sorts Workloads in place by Name (case-insensitive,
// deterministic, stable so equal names keep their merge order).
func sortStableByName(ws []Workload) {
	sort.SliceStable(ws, func(i, j int) bool {
		return strings.ToLower(ws[i].Name) < strings.ToLower(ws[j].Name)
	})
}

func sortManagedByName(ms []Managed) {
	sort.SliceStable(ms, func(i, j int) bool {
		return strings.ToLower(ms[i].Name) < strings.ToLower(ms[j].Name)
	})
}
