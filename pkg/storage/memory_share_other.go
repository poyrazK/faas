//go:build !linux

package storage

import "context"

type blockIndex struct{}

type peerIndex struct{}

func fileIdentity(path string) (string, error) { return identityString(path, 0, 0), nil }

func indexImageFiles(context.Context, []string) (*blockIndex, error) { return nil, nil }

func shareMemoryPages(context.Context, string, ...*blockIndex) (int64, error) { return 0, nil }

func memorySharedLevel(string) int { return 0 }

func setMemorySharedMarker(string, int) {}

// newPeerIndex returns nil: without FIDEDUPERANGE there is nothing to share.
func newPeerIndex() *peerIndex { return nil }

func (*peerIndex) prune() {}

func (*peerIndex) addFile(context.Context, string) error { return nil }

func (*peerIndex) share(context.Context, string) (int64, error) { return 0, nil }
