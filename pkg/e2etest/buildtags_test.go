package e2etest

import "testing"

// TestDaemonBuildTagsMatchesTestBinary — the harness must build its daemons
// with the same build tag the test binary was compiled with.
//
// spec: §4.5
// adr: 003
//
// The metal-tagged e2e tests drive real Firecracker through builderd and vmmd.
// When the harness built daemons without the tag, their metal paths compiled to
// the non-metal stubs and every build failed with
// "vm spawn: builderd: VM spawn is metal-only; use a fake VM in unit tests"
// (failure_class=infra) — a harness defect that reads as a platform one.
//
// This file has no build tag, so it is compiled both with and without `metal`
// and asserts the value is right in whichever configuration is running.
func TestDaemonBuildTagsMatchesTestBinary(t *testing.T) {
	if metalBuild != (daemonBuildTags == "metal") {
		t.Fatalf("metal build tag = %v but daemonBuildTags = %q; the daemons "+
			"would be built with different capabilities than the tests",
			metalBuild, daemonBuildTags)
	}
}
