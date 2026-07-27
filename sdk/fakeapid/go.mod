// fakeapid is the cross-language smoke fixture for the public SDKs
// (Go/Node/Python). It is stdlib-only on purpose: PR 5 (Node) and
// PR 6 (Python) spawn the same binary from their own CI smoke tests
// without a Go module dependency.
module github.com/poyrazK/faas/sdk/fakeapid

go 1.25
