//go:build unix

package wire

// adr: 20261020214632 — pre-traffic check fixtures.

import (
	"net"
	"os"
)

// UseHandoffProbesForTest installs fds as handed-off listeners with check
// connections: probes maps each check connection's descriptor to the
// network and address of the listener it is meant for.
func UseHandoffProbesForTest(fds []int, probes map[int][2]string) (restore func()) {
	prev := processActivation()
	set := inheritedSet(fds, nil, sourceHandoff, inspectInheritedFD)
	for fd, target := range probes {
		set.probes = append(set.probes, &handoffProbe{spec: checkFD{FD: fd, Network: target[0], Address: target[1]}})
	}
	activation = set
	return func() { activation = prev }
}

// UseHandoffChecksSkipFileForTest points the break-glass file elsewhere.
func UseHandoffChecksSkipFileForTest(path string) (restore func()) {
	prev := handoffChecksSkipFile
	handoffChecksSkipFile = path
	return func() { handoffChecksSkipFile = prev }
}

// RunHandoffCheckForTest sends check over f, the old generation's end of a
// check connection.
func RunHandoffCheckForTest(check HandoffCheck, f *os.File) error {
	return runHandoffCheck(check, f)
}

// PrepareHandoffChecksForTest prepares checks against listeners that serve
// reports, and returns the environment a successor would get from fd first.
func PrepareHandoffChecksForTest(checks []HandoffCheck, serves func(network, address string) bool, first int) (env []string, files int, err error) {
	hc, err := prepareHandoffChecks(checks, serves)
	if err != nil || hc == nil {
		return nil, 0, err
	}
	defer hc.close()
	return hc.env(first), len(hc.extraFiles()), nil
}

// ServesForTest reports whether this process shares a listener matching
// network/address.
func ServesForTest(network, address string) bool { return processActivation().serves(network, address) }

// ShareForTest records l as a shared listener.
func ShareForTest(l net.Listener) { processActivation().share(l) }
