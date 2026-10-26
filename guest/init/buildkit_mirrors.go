package main

import (
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// buildkitConfigPath is the BuildKit daemon config a builder writes when the
// build manifest names Docker Hub mirrors (ADR-20261025114554).
const buildkitConfigPath = "/etc/buildkit/buildkitd.toml"

// buildkitMirrorConfig renders the buildkitd.toml that sends Docker Hub pulls
// to mirrors first. BuildKit falls back to Docker Hub itself when every
// mirror fails. Hosts are validated again here: they are written into TOML.
func buildkitMirrorConfig(mirrors []string) (string, error) {
	if err := api.ValidDockerHubMirrors(mirrors); err != nil {
		return "", err
	}
	quoted := make([]string, len(mirrors))
	for i, host := range mirrors {
		quoted[i] = strconv.Quote(host)
	}
	return "[registry.\"docker.io\"]\n  mirrors = [" + strings.Join(quoted, ", ") + "]\n", nil
}
