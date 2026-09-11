// Command api-hosting-scorecard validates the API-hosting release evidence
// contract against the repository's capability registry and evidence files.
package main

import (
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/apihostingscorecard"
)

func main() {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "api-hosting-scorecard:", err)
		os.Exit(1)
	}
	scorecard, err := apihostingscorecard.Check(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "api-hosting-scorecard:", err)
		os.Exit(1)
	}
	evidence := 0
	targets := 0
	for _, gate := range scorecard.Gates {
		evidence += len(gate.Evidence)
		targets += len(gate.Targets)
	}
	fmt.Printf("api-hosting-scorecard: OK — %d gates, %d targets, %d evidence references\n", len(scorecard.Gates), targets, evidence)
}
