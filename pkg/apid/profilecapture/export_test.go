package profilecapture

// CreateProblem exposes createProblem to the external profilecapture_test
// package, so the store rejections that cannot be reached cheaply through a
// plan's real limits still have their problem shape pinned.
var CreateProblem = createProblem
