//go:build !linux

package main

// Execution remains supported, but automatic stale-lock recovery requires a
// process-domain and start-identity verifier. PID-only checks are insufficient.
func readTestCampaignProcessIdentity(pid int) (testCampaignProcessIdentity, error) {
	return testCampaignProcessIdentity{PID: pid}, nil
}

func probeTestCampaignProcess(identity testCampaignProcessIdentity) (string, string) {
	return "unverifiable", "automatic process verification is currently supported only on Linux"
}
