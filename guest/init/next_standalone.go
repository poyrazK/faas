package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sharedruntime"
)

// nextStandaloneHelperValue re-executes guest-init after `railpack prepare`
// to build a Next.js app with standalone output (ADR-20261124160000).
const nextStandaloneHelperValue = "next-standalone"

// nextStandaloneMaxPackageJSON bounds the package.json the helper reads.
const nextStandaloneMaxPackageJSON = 1 << 20

// nextStandaloneBuild reports whether a build asks for the standalone
// rewrite: a Railpack Node container build on the shared runner base, not a
// watch-mode developer image.
func nextStandaloneBuild(m api.BuildManifest) bool {
	return m.NextStandalone && m.SharedRuntime && !m.Function && m.DevWatchCommand == "" &&
		(m.Framework == api.FrameworkRailpackNode || m.Framework == api.FrameworkAuto)
}

// runNextStandalone rewrites the plan at args[0] for the app in args[1]. It
// never fails the build: an app it does not apply to, or a plan it cannot
// read, builds as Railpack planned it.
func runNextStandalone(args []string) int {
	if len(args) != 2 {
		return 0
	}
	if err := stampNextStandalonePlan(args[0], args[1]); err != nil && !errors.Is(err, sharedruntime.ErrNotNextStandalone) {
		fmt.Fprintf(os.Stderr, "Next.js standalone build not applied: %v\n", err)
	}
	return 0
}

func stampNextStandalonePlan(planPath, workdir string) error {
	pkg, err := readSmallRegularFile(filepath.Join(workdir, "package.json"), nextStandaloneMaxPackageJSON)
	if err != nil {
		return sharedruntime.ErrNotNextStandalone
	}
	plan, err := readSmallRegularFile(planPath, api.PublicBuildPlanMaxBytes)
	if err != nil {
		return err
	}
	stamped, err := sharedruntime.StampNextStandalone(plan, pkg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(planPath, stamped, 0o644); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Next.js: building with output \"standalone\" so the app ships only the files its server needs")
	return nil
}

func readSmallRegularFile(path string, maxBytes int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxBytes {
		return nil, fmt.Errorf("%s is not a regular file of at most %d bytes", filepath.Base(path), maxBytes)
	}
	return os.ReadFile(path) //nolint:forbidigo // path is inside the builder VM's build workspace.
}
