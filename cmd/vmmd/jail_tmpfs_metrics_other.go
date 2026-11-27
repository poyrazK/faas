//go:build !linux

package main

func statJailFS(string) (size, avail uint64, ok bool) { return 0, 0, false }
