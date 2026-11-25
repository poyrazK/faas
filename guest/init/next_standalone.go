package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

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
// watch-mode developer image, for a plan whose app layer cap the builder
// sent.
func nextStandaloneBuild(m api.BuildManifest) bool {
	return m.NextStandalone && m.SharedRuntime && m.AppLayerMaxMB > 0 && !m.Function && m.DevWatchCommand == "" &&
		(m.Framework == api.FrameworkRailpackNode || m.Framework == api.FrameworkAuto)
}

// runNextStandalone rewrites the plan at args[0] for the app in args[1],
// whose plan caps the app layer at args[2] MB. It never fails the build: an
// app it does not apply to, or a plan it cannot read, builds as Railpack
// planned it.
func runNextStandalone(args []string) int {
	if len(args) != 3 {
		return 0
	}
	capMB, err := strconv.Atoi(args[2])
	if err != nil {
		return 0
	}
	if err := stampNextStandalonePlan(args[0], args[1], capMB); err != nil && !errors.Is(err, sharedruntime.ErrNotNextStandalone) {
		fmt.Fprintf(os.Stderr, "Next.js standalone build not applied: %v\n", err)
	}
	return 0
}

func stampNextStandalonePlan(planPath, workdir string, capMB int) error {
	pkg, err := readSmallRegularFile(filepath.Join(workdir, "package.json"), nextStandaloneMaxPackageJSON)
	if err != nil {
		return sharedruntime.ErrNotNextStandalone
	}
	plan, err := readSmallRegularFile(planPath, api.PublicBuildPlanMaxBytes)
	if err != nil {
		return fmt.Errorf("read Railpack plan: %w", err)
	}
	stamped, err := sharedruntime.StampNextStandalone(plan, pkg, capMB, ownSize())
	if err != nil {
		return fmt.Errorf("stamp Railpack plan: %w", err)
	}
	if len(stamped) > api.PublicBuildPlanMaxBytes {
		// The public-build-URL helper that runs next refuses a larger plan.
		return fmt.Errorf("stamped plan is %d bytes, over %d: %w", len(stamped), api.PublicBuildPlanMaxBytes, sharedruntime.ErrNotNextStandalone)
	}
	// A temp file renamed over the plan: a failed write (a full disk) leaves
	// Railpack's plan whole rather than truncated.
	tmp, err := os.CreateTemp(filepath.Dir(planPath), ".railpack-plan-*.json")
	if err != nil {
		return fmt.Errorf("create stamped plan: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, writeErr := tmp.Write(stamped)
	if err := errors.Join(writeErr, tmp.Chmod(0o644), tmp.Close()); err != nil {
		return fmt.Errorf("write stamped plan: %w", err)
	}
	if err := os.Rename(tmp.Name(), planPath); err != nil {
		return fmt.Errorf("replace Railpack plan: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Next.js: building with output \"standalone\"; the app ships only its server's files if the whole app is over its plan's %d MB app layer\n", capMB)
	return nil
}

// ownSize is this guest-init's size, which every app layer carries beside
// /app; 0 when it cannot be read.
func ownSize() int64 {
	info, err := os.Stat("/proc/self/exe")
	if err != nil || !info.Mode().IsRegular() {
		return 0
	}
	return info.Size()
}

func readSmallRegularFile(path string, maxBytes int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", filepath.Base(path), err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxBytes {
		return nil, fmt.Errorf("%s is not a regular file of at most %d bytes", filepath.Base(path), maxBytes)
	}
	return os.ReadFile(path) //nolint:forbidigo // path is inside the builder VM's build workspace.
}
