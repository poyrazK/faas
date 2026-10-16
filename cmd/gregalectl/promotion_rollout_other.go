//go:build !linux

package main

import "os"

func preserveRolloutOwner(*os.File, os.FileInfo) error { return nil }
