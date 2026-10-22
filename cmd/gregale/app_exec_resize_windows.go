//go:build windows

package main

import "context"

// watchTerminalResize is a no-op on Windows, which has no SIGWINCH; the
// remote terminal keeps its initial size.
func watchTerminalResize(context.Context, func()) func() { return func() {} }
