//go:build metal && promotionacceptance

package e2etest

// Publication barriers must be present in the APID under test as well as the
// test binary. Ordinary metal builds retain their production no-op barrier.
const daemonBuildTags = "metal,promotionacceptance"
