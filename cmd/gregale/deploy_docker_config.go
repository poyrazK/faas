package main

// `gregale deploy --image REF --docker-config PATH` deploys a private image
// with the credential `docker login` stored for its registry. The image is
// pulled by Gregale, not by this machine, so the credential is saved to the
// app (as `gregale registry set` does) just before the deployment is
// submitted. Every later pull of that registry for the app uses it.

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
)

// deployRegistryCredential is a credential read from a Docker config, ready
// to save to the app under the host Gregale pulls the image from.
type deployRegistryCredential struct {
	apiHost string // e.g. registry-1.docker.io for Docker Hub images
	auth    *api.ImageRunRegistryAuth
}

// resolveDeployDockerConfig reads the image registry's credential before any
// network call, so a missing login fails the deploy before it changes
// anything.
func resolveDeployDockerConfig(ctx context.Context, configPath, image string, helper dockerCredentialHelper) (*deployRegistryCredential, error) {
	if configPath == "" {
		return nil, nil
	}
	if image == "" {
		return nil, errors.New("--docker-config needs --image: it supplies the credential for a registry image")
	}
	ref, err := oci.ParseReference(image)
	if err != nil {
		return nil, fmt.Errorf("image %q: %w", image, err)
	}
	auth, err := dockerConfigRegistryAuth(ctx, configPath, image, helper)
	if err != nil {
		return nil, err
	}
	return &deployRegistryCredential{apiHost: ref.APIHost(), auth: auth}, nil
}

// save stores the credential on the app so its image pull can use it.
func (c *deployRegistryCredential) save(ctx context.Context, client *Client, slug string) error {
	if c == nil {
		return nil
	}
	if _, err := client.SetAppRegistryCredential(ctx, slug, "https://"+c.apiHost, c.auth.Username, c.auth.Password); err != nil {
		return fmt.Errorf("save the %s credential to %s: %w", c.apiHost, slug, err)
	}
	if !jsonOutput {
		PrintProgress(osStderr, "saved the %s credential from the Docker config to %s", c.apiHost, slug)
	}
	return nil
}
