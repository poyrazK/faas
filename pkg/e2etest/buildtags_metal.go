//go:build metal

package e2etest

// daemonBuildTags is the -tags value the harness builds its daemon binaries
// with. It mirrors the tag the test binary itself was built with, because the
// daemons must have the same capabilities as the tests exercising them.
//
// Without it, `go test -tags metal ./cmd/e2e/...` spawned daemons compiled
// WITHOUT the tag, so their metal paths were the non-metal stubs:
//
//	builderd: build failed ... failure_class=infra
//	  "vm spawn: builderd: VM spawn is metal-only; use a fake VM in unit tests"
//
// Every build- and deploy-path test failed on the native gate for that reason
// (2026-09-14) after the spool-root fix let builds get as far as spawning a VM.
const daemonBuildTags = "metal"
