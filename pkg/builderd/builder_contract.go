package builderd

// guestInitBuildContractVersion versions what guest-init does inside a builder
// VM: guest/init's runBuild and everything it reaches. The build cache keys on
// this version instead of on the guest-init binary, whose bytes change on every
// release even when builds cannot.
//
// TestGuestInitBuildContractIsPinned fails when that code changes. Bump this
// version when the change can alter what a build produces, then pin the
// source digest the test reports.
const guestInitBuildContractVersion = 1
