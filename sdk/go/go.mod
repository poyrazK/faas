module github.com/poyrazK/faas/sdk/go

go 1.23

// Issue #266, PR 2. The SDK module is a leaf: ./internal/api contains the
// pkg/api/* wire client copies, while the public package adds SDK-specific
// helpers such as runtime Flags. The module uses only the standard library
// and has no dependency on github.com/onebox-faas/faas.
//
// No `require` for the root module here. The split in PR 12 will
// reverse: the daemon's root go.mod adds
// `require github.com/poyrazK/faas/sdk/go vX.Y.Z`, and pkg/api/* is trimmed
// to only the daemon-only files.
//
// Module path note: this is a nested module inside the monorepo, so the
// Go module proxy resolves its versions from tags prefixed with the
// module's directory — `sdk/go/v0.1.0`, NOT the repo-wide `v0.1.0`.
// A bare `v0.1.0` tag will not make `go get github.com/poyrazK/faas/sdk/go`
// resolve. See sdk/README-publishing.md.
