//go:build !linux

package storage

func mappedExtents(string) ([]mappedExtent, error) { return nil, errExtentsUnsupported }
