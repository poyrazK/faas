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

// viaDockerHubMirrors runs pull against each Docker Hub mirror in turn and
// then against r itself. Only anonymous Docker Hub references use a mirror.
// Every failure moves on to the next source, so a mirror that is down, slow
// to answer or missing the image costs one attempt; a cancelled context stops
// at once. Content checks are unchanged: blobs and digest-pinned manifests
// are verified against their digests whichever host served them.
func viaDockerHubMirrors[T any](ctx context.Context, c *RegistryClient, r Reference, auth *BasicAuth, pull func(Reference) (T, error)) (T, error) {
	if auth != nil || r.Registry != defaultRegistry {
		return pull(r)
	}
	for _, host := range c.dockerHubMirrors {
		mirrored := r
		mirrored.Registry = host
		v, err := pull(mirrored)
		if err == nil || ctx.Err() != nil {
			return v, err
		}
		if c.onMirrorFallback != nil {
			c.onMirrorFallback(host, err)
		}
	}
	return pull(r)
}
