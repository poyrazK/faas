package main

import (
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// buildkitConfigPath is the BuildKit daemon config a builder writes when the
// build manifest names Docker Hub mirrors (ADR-20261025114554) or vmmd serves
// the build private registry mirrors (ADR-20261110210000 slice 3j-3).
const buildkitConfigPath = "/etc/buildkit/buildkitd.toml"

// buildkitMirrorConfig renders the buildkitd.toml that sends Docker Hub pulls
// to mirrors first. BuildKit falls back to Docker Hub itself when every
// mirror fails. Hosts are validated again here: they are written into TOML.
func buildkitMirrorConfig(mirrors []string) (string, error) {
	return buildkitRegistryConfig(mirrors, nil)
}

// buildkitRegistryConfig renders buildkitd.toml for the operator's Docker Hub
// mirrors and the build's private registry mirrors. A private registry
// pulls from its mirror, 127.0.0.1:GuestPort over plain HTTP (guest-init
// forwards it to vmmd on vsock), before the registry itself; Docker Hub
// lists a private mirror ahead of the operator's. Both lists are validated
// again here, since they are written into TOML.
func buildkitRegistryConfig(hubMirrors []string, private []api.BuildRegistryMirror) (string, error) {
	if err := api.ValidDockerHubMirrors(hubMirrors); err != nil {
		return "", err
	}
	if err := api.ValidBuildRegistryMirrors(private); err != nil {
		return "", err
	}
	mirrorsOf := map[string][]string{}
	var order []string
	add := func(registry, mirror string) {
		if _, ok := mirrorsOf[registry]; !ok {
			order = append(order, registry)
		}
		mirrorsOf[registry] = append(mirrorsOf[registry], mirror)
	}
	for _, m := range private {
		registry := m.Registry
		switch registry {
		case "registry-1.docker.io", "index.docker.io":
			registry = "docker.io"
		}
		add(registry, "127.0.0.1:"+strconv.Itoa(m.GuestPort))
	}
	for _, host := range hubMirrors {
		add("docker.io", host)
	}
	var b strings.Builder
	for _, registry := range order {
		quoted := make([]string, len(mirrorsOf[registry]))
		for i, host := range mirrorsOf[registry] {
			quoted[i] = strconv.Quote(host)
		}
		b.WriteString("[registry." + strconv.Quote(registry) + "]\n  mirrors = [" + strings.Join(quoted, ", ") + "]\n")
	}
	for _, m := range private {
		b.WriteString("[registry." + strconv.Quote("127.0.0.1:"+strconv.Itoa(m.GuestPort)) + "]\n  http = true\n")
	}
	return b.String(), nil
}
