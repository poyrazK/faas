module github.com/gregale-dev/gregale-go

go 1.23

// The SDK module is a leaf: ./internal/api holds copies of the pkg/api wire
// client, and the module uses only the standard library. It has no
// dependency on github.com/onebox-faas/faas.
//
// This directory is the source of truth. scripts/sdk_mirror.py publishes it
// to github.com/gregale-dev/gregale-go, where the module path resolves and
// versions are plain vX.Y.Z tags. See sdk/README-publishing.md.
