//go:build !metal

package e2etest

// daemonBuildTags is empty off the metal build tag: the daemons are built
// exactly as before, so non-metal e2e runs are unaffected. See
// buildtags_metal.go for why this exists.
const daemonBuildTags = ""
