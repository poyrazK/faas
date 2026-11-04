package main

import (
	"os"
	"os/signal"
	"syscall"
)

// signal.Notify broadcasts to registrations, while a channel delivers each
// value to one reader. Each policy reloader therefore owns its own channel.
func policyBundleSignals() (chan os.Signal, chan os.Signal, func()) {
	egress := make(chan os.Signal, 1)
	static := make(chan os.Signal, 1)
	signal.Notify(egress, syscall.SIGHUP)
	signal.Notify(static, syscall.SIGHUP)
	return egress, static, func() { signal.Stop(egress); signal.Stop(static) }
}
