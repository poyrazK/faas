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
		if profile.Framework != want.Framework || profile.PackageManager != want.PackageManager || profile.Port != want.Port || profile.HealthPath != want.HealthPath || profile.StartCommand != want.StartCommand || profile.ConfigFile != want.ConfigFile || profile.Inferred != want.Inferred {
			fail(fmt.Errorf("%s: got framework=%q package_manager=%q port=%d health=%q command=%q config=%q inferred=%t; want framework=%q package_manager=%q port=%d health=%q command=%q config=%q inferred=%t", fixture.ID, profile.Framework, profile.PackageManager, profile.Port, profile.HealthPath, profile.StartCommand, profile.ConfigFile, profile.Inferred, want.Framework, want.PackageManager, want.Port, want.HealthPath, want.StartCommand, want.ConfigFile, want.Inferred))
		}
		fmt.Printf("ok %-16s framework=%-10s port=%d\n", fixture.ID, profile.Framework, profile.Port)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "api-hosting-contract:", err)
	os.Exit(1)
}
