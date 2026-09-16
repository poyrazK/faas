//go:build metal

package e2etest

// metalBuild reports whether this package was compiled with the metal build
// tag. It is a separate declaration from daemonBuildTags on purpose: the test
// compares the two, so deriving one from the other would make the check
// vacuous.
const metalBuild = true
