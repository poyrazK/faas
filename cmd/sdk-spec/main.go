// Command sdk-spec generates sdk/openapi.yaml, the spec the published SDKs
// are generated from (ADR-20261023115741): api/openapi.yaml without the
// operator routes, API monetization, and the paths of every preview or
// internal capability (by tier, or through an ancestor). The SDKs ship what
// is generally available or beta; apid, the CLI and the contract gates keep
// using the complete api/openapi.yaml.
package main

import (
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/apid"
	"github.com/onebox-faas/faas/pkg/apid/capgate"
	"github.com/onebox-faas/faas/pkg/productcap"
)

func main() {
	out, err := render()
	if err != nil {
		fmt.Fprintln(os.Stderr, "sdk-spec:", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(out); err != nil {
		fmt.Fprintln(os.Stderr, "sdk-spec: write:", err)
		os.Exit(1)
	}
}

// render filters the complete spec by the catalog's tier defaults: with no
// flag snapshot, a capability is on only when it and its ancestors are GA or
// beta.
func render() ([]byte, error) {
	catalog, err := productcap.Load()
	if err != nil {
		return nil, err
	}
	gate := capgate.New(catalog, nil)
	hidden := map[string]bool{}
	for _, capability := range catalog.Capabilities {
		if !gate.ReleasedByDefault(capability.ID) {
			hidden[capability.ID] = true
		}
	}
	return apid.FilterSpec(apid.OpenAPIYAML(), func(path string) bool { return gate.SpecHidden(path, hidden) }, false), nil
}
