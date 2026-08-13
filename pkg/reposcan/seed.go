package reposcan

// detector is the named source that produced a workloadSeed. The
// merge rule uses this as a tiebreak: when two seeds at the same
// tier have the same (RootDir, Name), the lower-detectorPriority
// wins identity (and first-non-empty wins per field). The detector
// is a fixed enum — NOT a free-form source string — so detector
// precedence is a contract detectors cannot accidentally
// renegotiate by changing their source-string format.
type detector uint8

const (
	detOther      detector = iota // convention / workspaces / root floor
	detCompose                    // compose.yaml
	detProcfile                   // Procfile
	detK8s                        // k8s manifests
	detRender                     // render.yaml
	detFly                        // fly.toml
	detServerless                 // serverless.yml
	detAppYaml                    // app.yaml
)

// detectorPriority is the canonical tiebreak order. Within a tier,
// higher priority wins (Procfile is the most explicit name
// declarer; compose is the most common workload carrier). The
// sentinel values are NON-CONTIGUOUS so a future detector can
// land between two existing ones without renumbering.
func (d detector) priority() uint8 {
	switch d {
	case detCompose:
		return 80
	case detProcfile:
		return 75
	case detK8s:
		return 70
	case detRender:
		return 65
	case detFly:
		return 60
	case detServerless:
		return 55
	case detAppYaml:
		return 50
	}
	return 0 // convention / workspaces / root floor
}

// String is the wire name for the detector (issue #742). It is a
// CLOSED vocabulary surfaced on PlanWorkload.detected_by.detector
// and pinned by the OpenAPI enum in api/openapi.yaml — renaming a
// value is a wire-contract change, not a refactor.
//
// detOther is the floor bucket (convention scan, workspace graph,
// root Dockerfile). It reports "other" rather than "" so a consumer
// never has to distinguish "no detector" from "the floor detector";
// every workload the scanner emits came from some detector.
func (d detector) String() string {
	switch d {
	case detCompose:
		return detNameCompose
	case detProcfile:
		return detNameProcfile
	case detK8s:
		return detNameK8s
	case detRender:
		return detNameRender
	case detFly:
		return detNameFly
	case detServerless:
		return detNameServerless
	case detAppYaml:
		return detNameAppYaml
	case detOther:
		return detNameOther
	}
	return detNameOther
}

// Detector wire names. Kept as named constants rather than inline
// literals because `compose` also appears as a Tier name
// (scan.go::Tier.String) and as a YAML tag (compose.go) — three
// unrelated vocabularies sharing a spelling. Naming them makes a
// future rename of ONE of the three a compile-scoped edit instead of
// a grep-and-hope, and satisfies goconst.
const (
	detNameCompose    = "compose"
	detNameProcfile   = "procfile"
	detNameK8s        = "k8s"
	detNameRender     = "render"
	detNameFly        = "fly"
	detNameServerless = "serverless"
	detNameAppYaml    = "app_yaml"
	detNameOther      = "other"
)

// workloadSeed is the internal carrier produced by each tier
// detector before merge.go collapses them into a sorted
// []Workload. Keeping it lighter than Workload makes per-tier
// code shorter (the merge rule fills the empty fields).
type workloadSeed struct {
	tier       Tier
	det        detector
	source     string // provenance string; carried into Workload.Source
	name       string
	rootDir    string
	dockerfile string
	command    []string
	class      Class
	schedule   string
	ports      []int
	envKeys    []string // KEYS only
}

// workloadKey is the merge-by-(RootDir, Name) key. Two seeds with
// the same Key collapse into one Workload during merge.
type workloadKey struct {
	RootDir string
	Name    string
}

func (s workloadSeed) key() workloadKey {
	return workloadKey{RootDir: s.rootDir, Name: s.name}
}

// seedWarning returns a deterministic warning line emitted for a
// seed that came from a non-default source. Keeps the Warning list
// useful for operator debugging without bloating it.
func seedWarning(s workloadSeed) string {
	if s.name == "" {
		return ""
	}
	return "reposcan: " + s.source + ": workload=" + s.name
}
