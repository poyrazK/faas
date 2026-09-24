package preflight

import (
	"fmt"
	"io/fs"

	"github.com/onebox-faas/faas/pkg/frameworkprofile"
)

// Assess runs the full static check over a source tree: framework inference
// first, then the contract disqualifiers. The worst finding sets the headline
// level, so a single red outranks any number of ambers.
func Assess(fsys fs.FS) (Verdict, error) {
	profile, err := frameworkprofile.Analyze(fsys)
	if err != nil {
		return Verdict{}, fmt.Errorf("preflight analyze source: %w", err)
	}
	verdict := Evaluate(profile)
	for _, finding := range ScanContract(fsys) {
		verdict.Findings = append(verdict.Findings, finding)
		verdict.Level = worst(verdict.Level, finding.Level)
	}
	return verdict, nil
}
