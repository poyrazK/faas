package mcpmanagement

import "testing"

func TestHostedPrivateReadinessPublicationJourney(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		for _, runtime := range []string{"node", "go", "python"} {
			for _, archive := range []bool{false, true} {
				t.Run(version+"/"+runtime+map[bool]string{false: "/inline", true: "/archive"}[archive], func(t *testing.T) { hostedToolPublicationJourney(t, version, runtime, archive, true, true) })
			}
		}
	}
}
