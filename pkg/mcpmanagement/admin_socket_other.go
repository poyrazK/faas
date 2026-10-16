//go:build !unix

package mcpmanagement

import (
	"errors"
	"net"
	"net/http"
)

// ValidateAdminSocketPath reports that owner-protected sockets require Unix.
func ValidateAdminSocketPath(string) error {
	return errors.New("management admin sockets require a Unix platform")
}

// ListenAdminSocket reports that owner-protected sockets require Unix.
func ListenAdminSocket(string) (net.Listener, error) { return nil, ValidateAdminSocketPath("") }

// AdminClient reports that owner-protected sockets require Unix.
func AdminClient(string) (*http.Client, error) { return nil, ValidateAdminSocketPath("") }
