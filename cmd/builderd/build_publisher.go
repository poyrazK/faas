package main

import (
	"fmt"
	"github.com/onebox-faas/faas/pkg/buildpublisher"
	"os"
)

// Both values are explicit. Existing platform signing keys are never selected
// by default, and loading a private key never installs an app trusted signer.
func configuredBuildPublisher(getenv func(string) string) (buildpublisher.Signer, error) {
	name, path := getenv("FAAS_BUILD_PUBLISHER_NAME"), getenv("FAAS_BUILD_PUBLISHER_KEY")
	if name == "" && path == "" {
		return nil, nil
	}
	if name == "" || path == "" {
		return nil, fmt.Errorf("builderd: both build publisher name and key are required")
	}
	signer, err := buildpublisher.NewFileSigner(name, path)
	if err != nil {
		return nil, fmt.Errorf("builderd: invalid build publisher key")
	}
	return signer, nil
}

func loadBuildPublisher() (buildpublisher.Signer, error) { return configuredBuildPublisher(os.Getenv) }
