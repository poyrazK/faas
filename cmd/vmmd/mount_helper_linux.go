//go:build linux

package main

import (
	"os"

	"github.com/onebox-faas/faas/pkg/jailsetup"
)

// Retain the original command modes for older releases without a separate helper.
func runMountBindHelper() bool { return jailsetup.Run(os.Args) }
