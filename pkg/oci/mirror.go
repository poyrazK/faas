package oci

import "context"

// WithDockerHubMirrors makes anonymous Docker Hub pulls try each mirror host,
// in order, before Docker Hub itself (ADR-20261025114554), as Docker's
// registry-mirrors setting does. A pull that carries a registry credential
// never goes to a mirror. onFallback, when set, hears each mirror failure
// that sent the pull on to the next source.
func WithDockerHubMirrors(hosts []string, onFallback func(mirror string, err error)) Option {
	return func(c *RegistryClient) {
		c.dockerHubMirrors = append([]string(nil), hosts...)
		c.onMirrorFallback = onFallback
	}
}

// pullFrom runs pull for r with the credential the registry accepts
// (registryAuth, ADR-20261025124445). An anonymous Docker Hub pull runs
// against each Docker Hub mirror in turn and then against Docker Hub itself.
// Every mirror failure moves on to the next source, so a mirror that is down,
// slow to answer or missing the image costs one attempt; a cancelled context
// stops at once. Content checks are unchanged: blobs and digest-pinned
// manifests are verified against their digests whichever host served them.
func pullFrom[T any](ctx context.Context, c *RegistryClient, r Reference, auth *BasicAuth, pull func(at Reference, auth *BasicAuth) (T, error)) (T, error) {
	auth, err := c.registryAuth(ctx, r, auth)
	if err != nil {
		var zero T
		return zero, err
	}
	if auth != nil || r.Registry != defaultRegistry {
		return pull(r, auth)
	}
	for _, host := range c.dockerHubMirrors {
		mirrored := r
		mirrored.Registry = host
		v, err := pull(mirrored, nil)
		if err == nil || ctx.Err() != nil {
			return v, err
		}
		if c.onMirrorFallback != nil {
			c.onMirrorFallback(host, err)
		}
	}
	return pull(r, nil)
}
