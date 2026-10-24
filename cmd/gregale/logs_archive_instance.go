package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// fullInstanceIDLen is the length of a canonical instance UUID.
const fullInstanceIDLen = 36

// resolveArchiveInstance expands a short instance prefix to the full id of
// one of the app's instances. Log lines print the first 8 characters of an
// instance id, and passing that prefix to --archive --instance used to come
// back as "No archived logs were found ... (archive gap)" because archives
// are keyed by the full id (Hunt 12 H12-8). A full id, or a prefix that
// matches no instance, is returned unchanged so the server's answer stands;
// a prefix that matches several instances is an error.
func resolveArchiveInstance(ctx context.Context, client *Client, slug, instance string) (string, error) {
	if len(instance) >= fullInstanceIDLen || !isHexPrefix(instance) {
		return instance, nil
	}
	instances, err := client.ListInstancesWithHistory(ctx, slug, true)
	if err != nil {
		return "", fmt.Errorf("list instances to expand %q: %w", instance, err)
	}
	var matches []string
	for _, inst := range instances {
		if strings.HasPrefix(inst.ID, instance) {
			matches = append(matches, inst.ID)
		}
	}
	switch len(matches) {
	case 0:
		return instance, nil
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("instance prefix %q matches %d instances (%s); pass more characters", instance, len(matches), strings.Join(matches, ", "))
	}
}

func isHexPrefix(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdef-", r) {
			return false
		}
	}
	return true
}

// expandArchiveInstance applies resolveArchiveInstance to selector in place
// and returns a non-zero exit code when it printed an error.
func expandArchiveInstance(slug string, selector *api.ArchiveLogSelector) int {
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	full, err := resolveArchiveInstance(context.Background(), client, slug, selector.InstanceID)
	if err != nil {
		return printErr("Could not resolve instance", err)
	}
	selector.InstanceID = full
	return 0
}
