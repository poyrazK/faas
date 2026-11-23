//go:build windows

package main

// lockReleaseInstalls is a no-op on Windows: release install runs only on
// Linux hosts; the stub keeps `go build ./...` working on a Windows box.
func lockReleaseInstalls(string) (func(), error) {
	return func() {}, nil
}
