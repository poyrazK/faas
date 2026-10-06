//go:build linux

package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/onebox-faas/faas/pkg/ociidentity"
)

// processCredential confines named user/group lookup to the workload's image.
// OpenRoot prevents a companion identity-file symlink from escaping its root.
func processCredential(root, user string) (*syscall.Credential, error) {
	name, _, _ := strings.Cut(user, ":")
	fallbackUID := legacyLookupUID(name)
	if root == "" {
		root = "/"
	} else {
		fallbackUID = legacyLookupUIDInRoot(root, name)
	}
	image, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open process image root: %w", err)
	}
	defer func() { _ = image.Close() }()
	id, err := ociidentity.Resolve(image.FS(), user, ociidentity.Identity{UID: uint32(fallbackUID), GID: uint32(fallbackUID)})
	if err != nil {
		return nil, fmt.Errorf("resolve process identity: %w", err)
	}
	return &syscall.Credential{Uid: id.UID, Gid: id.GID}, nil
}

// UID-only consumers (secret projections and guest metadata gates) must agree
// with the credentials used at exec, including image-local named users.
func lookupUID(user string) int {
	credential, err := processCredential("", user)
	if err != nil {
		return legacyLookupUID(user)
	}
	return int(credential.Uid)
}

func lookupUIDInRoot(root, user string) int {
	credential, err := processCredential(root, user)
	if err != nil {
		return legacyLookupUIDInRoot(root, user)
	}
	return int(credential.Uid)
}

// UID/GID zero keeps the established PID-1-root launch behavior. A root user
// with a nonzero explicit group still requires a credential transition.
func execProcessCredential(credential *syscall.Credential) *syscall.Credential {
	if credential.Uid == 0 && credential.Gid == 0 {
		return nil
	}
	return credential
}
