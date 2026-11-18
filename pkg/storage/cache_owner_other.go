//go:build !unix

package storage

// lockEvictionOwner is a no-op where advisory locks are not used: the node
// cache runs on Linux; other builds (the CLI) never share one.
func lockEvictionOwner(string) (func(), error) { return func() {}, nil }

func evictionOwnerHeld(string) bool { return false }
