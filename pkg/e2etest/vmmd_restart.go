package e2etest

// RestartVMMD replaces the owned harness child using its original launch
// environment. The runtime owns adoption of surviving guest processes.
func (h *Harness) RestartVMMD() error { return h.restartNativeOwner("vmmd") }
