//go:build !linux

package storage

import (
	"context"
	"os"
)

func fileExtents(string) ([]physicalExtent, error) { return nil, errExtentsUnsupported }

func cloneInto(*os.File, *os.File) bool { return false }

func dedupeFile(context.Context, string, string) (int64, error) { return 0, nil }
