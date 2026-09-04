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

// String makes Tier printable for confirm tables and warning logs.
func (t Tier) String() string {
	switch t {
	case TierSingle:
		return "single"
	case TierConvention:
		return "convention"
	case TierWorkspace:
		return "workspace"
	case TierCompose:
		return "compose"
	default:
		return "unknown"
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
// missing source file is a no-op, not an error. The Tier-4 root
// floor is used only when the root contains a deployable marker;
// an empty or documentation-only archive must not masquerade as an
// application. The only error path is invalid-path escape attempts,
// which fail closed so a malicious tarball can never reach the host.
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

	// Tier 4 — root floor. Do not manufacture an app for an empty or
	// unrelated repository. This result is consumed by reconcile, so a
	// synthetic root workload would otherwise create a real app from a
	// README-only or malformed source archive.
	if len(seeds) == 0 && hasRootFloorMarker(fsys) {
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

	return Result{
		Workloads: workloads,
		Managed:   managed,
		Tier:      highestTier,
		Warnings:  warnings,
	}, nil
}

// hasRootFloorMarker reports whether the archive has enough source shape to
// be treated as one root application. Detector-specific manifests already
// produce seeds above; this list covers the simple single-project markers
// that intentionally use the root-floor fallback.
func hasRootFloorMarker(fsys fs.FS) bool {
	for _, marker := range []string{
		"Dockerfile", "dockerfile", "package.json", "requirements.txt",
		"pyproject.toml", "Pipfile", "pipfile", "setup.py", "go.mod",
		"Cargo.toml", "pom.xml",
	} {
		info, err := fs.Stat(fsys, marker)
		if err == nil && !info.IsDir() {
			return true
		}
	}
	return false
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
