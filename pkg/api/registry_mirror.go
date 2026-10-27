package api

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// DockerHubMirrorsEnv names the operator's Docker Hub mirrors: a
// comma-separated list of registry hosts that serve Docker Hub content, such
// as mirror.gcr.io or a pull-through cache (ADR-20261025114554). imaged pulls
// anonymous Docker Hub images through them, and builderd hands them to the
// BuildKit daemon of every builder VM. Each mirror is tried in order before
// Docker Hub itself.
const DockerHubMirrorsEnv = "FAAS_DOCKER_HUB_MIRRORS"

var dockerHubMirrorHost = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*(:[0-9]{1,5})?$`)

// ParseDockerHubMirrors parses a DockerHubMirrorsEnv value. Each entry is a
// lowercase registry host with an optional port, reached over HTTPS; a
// scheme, path or Docker Hub itself is refused. Empty means no mirror.
func ParseDockerHubMirrors(value string) ([]string, error) {
	var mirrors []string
	seen := map[string]bool{}
	for _, entry := range strings.Split(value, ",") {
		host := strings.TrimSpace(entry)
		if host == "" {
			continue
		}
		if err := validDockerHubMirror(host); err != nil {
			return nil, fmt.Errorf("%s: %w", DockerHubMirrorsEnv, err)
		}
		if !seen[host] {
			seen[host] = true
			mirrors = append(mirrors, host)
		}
	}
	if len(mirrors) > MaxDockerHubMirrors {
		return nil, fmt.Errorf("%s: %d mirrors, at most %d", DockerHubMirrorsEnv, len(mirrors), MaxDockerHubMirrors)
	}
	return mirrors, nil
}

// ValidDockerHubMirrors reports whether every entry of a build manifest's
// mirror list is a host ParseDockerHubMirrors accepts.
func ValidDockerHubMirrors(mirrors []string) error {
	if len(mirrors) > MaxDockerHubMirrors {
		return fmt.Errorf("%d Docker Hub mirrors, at most %d", len(mirrors), MaxDockerHubMirrors)
	}
	for _, host := range mirrors {
		if err := validDockerHubMirror(host); err != nil {
			return err
		}
	}
	return nil
}

func validDockerHubMirror(host string) error {
	if len(host) > 253 || !dockerHubMirrorHost.MatchString(host) {
		return fmt.Errorf("mirror %q must be a lowercase registry host with an optional port, without a scheme or path", host)
	}
	if name, port, ok := strings.Cut(host, ":"); ok {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("mirror %q has an invalid port", host)
		}
		host = name
	}
	switch host {
	case "docker.io", "registry-1.docker.io", "index.docker.io":
		return fmt.Errorf("mirror %q is Docker Hub itself", host)
	}
	return nil
}
