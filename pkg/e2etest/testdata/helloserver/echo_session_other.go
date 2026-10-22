//go:build !linux

package main

import "os"

// runEchoSession is Linux-only; the fixture image is always linux/amd64.
func runEchoSession() { os.Exit(2) }
