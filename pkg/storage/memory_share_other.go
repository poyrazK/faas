//go:build !linux

package storage

import "context"

type blockIndex struct{}

func fileIdentity(path string) (string, error) { return identityString(path, 0, 0), nil }

func indexImageFiles(context.Context, []string) (*blockIndex, error) { return nil, nil }

func shareMemoryPages(context.Context, string, ...*blockIndex) (int64, error) { return 0, nil }

func memorySharedMarker(string) bool { return false }

func setMemorySharedMarker(string) {}
