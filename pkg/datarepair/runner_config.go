package datarepair

import "strings"

// RunnerFromEnv reads only release-owned, non-secret artifact metadata. Empty
// disables creation. Partial configuration fails closed on either daemon.
func RunnerFromEnv(getenv func(string) string) (RunnerSpec, bool, error) {
	value := func(name string) string { return strings.TrimSpace(getenv("FAAS_DATA_REPAIR_RUNNER_" + name)) }
	s := RunnerSpec{BaseKey: value("BASE_KEY"), LayerKey: value("LAYER_KEY"), KernelSHA256: value("KERNEL_SHA256"), BaseSHA256: value("BASE_SHA256"), LayerSHA256: value("LAYER_SHA256")}
	if s == (RunnerSpec{}) {
		return s, false, nil
	}
	return s, true, s.Validate()
}
