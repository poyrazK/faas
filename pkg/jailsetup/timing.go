package jailsetup

// SetupJailTimingPrefix is the stdout marker --setup-jail writes with its own
// in-namespace duration in microseconds.
//
// vmmd's bind_tun window measured ~30 ms on an idle SSD acceptance node, of
// which the single nsenter invocation was ~22 ms — roughly a quarter of the
// whole snapshot restore. The work that invocation performs is a handful of
// mount/mknod syscalls, so the open question is how much of the 22 ms is
// process-spawn overhead (nsenter, then the helper it execs, forked from a
// ~79 MB Go parent) rather than the work itself. Self-reporting answers that
// without adding a second control spawn to measure against.
//
// This lives outside run_linux.go so the parser in pkg/fcvm, which builds on
// every platform, can reference the same constant as the writer.
const SetupJailTimingPrefix = "faas-jail-setup-us="
