// Command api-hosting-contract runs the production-shaped API fixture catalog
// locally. It is intentionally metal-free: this gate validates source
// detection and profile inference before the reference-node boot suite.
package main

import (
	"fmt"
	"os"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/apihostingcontract"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
)

func main() {
	catalog, err := apihostingcontract.Load()
	if err != nil {
		fail(err)
	}
	for _, fixture := range catalog.Fixtures {
		files := make(fstest.MapFS, len(fixture.Files))
		for path, body := range fixture.Files {
			files[path] = &fstest.MapFile{Data: []byte(body)}
		}
		profile, err := frameworkprofile.Analyze(files)
		if err != nil {
			fail(fmt.Errorf("%s: %w", fixture.ID, err))
		}
		want := fixture.Expected
		if profile.Framework != want.Framework || profile.Port != want.Port || profile.StartCommand != want.StartCommand || profile.Inferred != want.Inferred {
			fail(fmt.Errorf("%s: got framework=%q port=%d command=%q inferred=%t; want framework=%q port=%d command=%q inferred=%t", fixture.ID, profile.Framework, profile.Port, profile.StartCommand, profile.Inferred, want.Framework, want.Port, want.StartCommand, want.Inferred))
		}
		fmt.Printf("ok %-16s framework=%-10s port=%d\n", fixture.ID, profile.Framework, profile.Port)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "api-hosting-contract:", err)
	os.Exit(1)
}
