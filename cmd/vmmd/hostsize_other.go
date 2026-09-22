//go:build !linux

package main

import "github.com/onebox-faas/faas/pkg/hostsize"

func hostMemTotalMB() int { return hostsize.MemTotalMB() }

func hostCPUs() int { return hostsize.CPUs() }
