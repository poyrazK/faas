// Package preflight answers one question about a source tree: would this app
// run on Gregale? It is deliberately static — no build, no VM, no execution of
// customer code — so the answer is cheap enough to give an anonymous visitor.
//
// The verdict is intentionally conservative. A false green costs a user; a
// false amber costs a sentence of explanation.
package preflight

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
)

// The wire contract lives in pkg/api so the console and external callers bind
// to one set of types. These aliases keep the analysis code readable without
// introducing a second shape to convert between.
type (
	// Level is the headline verdict for a source tree.
	Level = api.PreflightLevel
	// Finding is one actionable observation about the source tree.
	Finding = api.PreflightFinding
	// Verdict is the complete preflight answer for one source tree.
	Verdict = api.PreflightVerdict
	// Source is a validated public GitHub repository reference.
	Source = api.PreflightSource
	// PlanBudget is what one plan buys, stated as running time.
	PlanBudget = api.PreflightPlanBudget
	// Report is one complete answer, pinned to a commit.
	Report = api.PreflightReport
)

const (
	// LevelGreen means the source already satisfies the container contract.
	LevelGreen = api.PreflightGreen
	// LevelAmber means the source runs once a declared change is supplied.
	LevelAmber = api.PreflightAmber
	// LevelRed means a hard disqualifier from the container contract applies.
	LevelRed = api.PreflightRed
)

// Evaluate maps an inferred profile onto the container contract. It considers
// only what static inference can see; contract disqualifiers that live in a
// Dockerfile or compose file are added by ScanContract.
func Evaluate(profile frameworkprofile.Profile) Verdict {
	verdict := Verdict{Level: LevelGreen, Profile: wireProfile(profile)}
	// Inference emits one warning per file, so a real repository yields the
	// same code many times over. The report states each problem once and
	// gathers the files under it; a wall of identical entries is noise to the
	// person deciding whether to migrate.
	index := make(map[string]int, len(profile.Warnings))
	for _, w := range profile.Warnings {
		if at, seen := index[w.Code]; seen {
			verdict.Findings[at].Sources = appendCappedSources(verdict.Findings[at].Sources, w.Sources)
			continue
		}
		finding := findingForWarning(w)
		finding.Sources = appendCappedSources(nil, finding.Sources)
		index[w.Code] = len(verdict.Findings)
		verdict.Findings = append(verdict.Findings, finding)
		verdict.Level = worst(verdict.Level, finding.Level)
	}
	return verdict
}

// maxFindingSources bounds the paths listed under one finding. Past a handful
// the list stops informing and starts burying the remedy.
const maxFindingSources = 10

func appendCappedSources(existing, incoming []string) []string {
	for _, source := range incoming {
		if len(existing) >= maxFindingSources {
			return existing
		}
		existing = append(existing, source)
	}
	return existing
}

// wireProfile projects the analyzer's result onto the published contract. The
// warning list is deliberately dropped: warnings reach the caller as findings,
// with a remedy attached, rather than twice in two shapes.
func wireProfile(profile frameworkprofile.Profile) api.PreflightProfile {
	return api.PreflightProfile{
		Version:        profile.Version,
		Framework:      profile.Framework,
		FrameworkVer:   profile.FrameworkVer,
		PackageManager: profile.PackageManager,
		DockerfilePath: profile.DockerfilePath,
		StartCommand:   profile.StartCommand,
		Port:           profile.Port,
		HealthPath:     profile.HealthPath,
		ConfigFile:     profile.ConfigFile,
		Inferred:       profile.Inferred,
	}
}
