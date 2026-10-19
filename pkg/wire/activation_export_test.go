package wire

// UseInheritedListenersForTest installs fds as this process's inherited
// sockets, as if systemd had passed them. The set takes ownership of each
// descriptor it hands out. restore reinstates the previous set.
func UseInheritedListenersForTest(fds []int, names []string) (restore func()) {
	prev := processActivation()
	activation = inheritedSet(fds, names, sourceSystemd, inspectInheritedFD)
	return func() { activation = prev }
}

// LoadActivationForTest parses an activation environment. inspect stands in
// for getsockname on the descriptors it names.
func LoadActivationForTest(env map[string]string, pid int, inspect func(fd int) (string, string, error)) (sockets []string, err error) {
	set := loadActivation(func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	}, pid, inspect)
	for _, sock := range set.sockets {
		sockets = append(sockets, sock.String())
	}
	return sockets, set.err
}

// UseHandoffListenersForTest installs fds as if a predecessor had handed
// them over.
func UseHandoffListenersForTest(fds []int) (restore func()) {
	prev := processActivation()
	activation = inheritedSet(fds, nil, sourceHandoff, inspectInheritedFD)
	return func() { activation = prev }
}

// UseHandoffReadyForTest substitutes the channel that releases a hand-off
// successor's listeners.
func UseHandoffReadyForTest(ready <-chan struct{}) (restore func()) {
	prev := handoffReady
	handoffReady = func() <-chan struct{} { return ready }
	return func() { handoffReady = prev }
}
