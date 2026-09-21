//go:build linux

package main

import "github.com/onebox-faas/faas/pkg/hostsize"

// hostMemTotalMB and hostCPUs delegate to pkg/hostsize so vmmd and
// gregalectl probe the machine identically. gregalectl publishes the derived
// sizing to the deploy layer, so a second copy of this probe here would let
// the admission ceiling and the cgroup fence drift apart.
func hostMemTotalMB() int { return hostsize.MemTotalMB() }

func hostCPUs() int { return hostsize.CPUs() }
