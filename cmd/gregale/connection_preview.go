package main

import (
	"fmt"
	"net/url"
)

type previewConnection struct {
	*connectionContext
	Verified        bool `json:"verified"`
	AddressRedacted bool `json:"address_redacted"`
}

type displayConnection struct {
	*connectionContext
	AddressRedacted bool `json:"address_redacted"`
}

func configuredPreviewConnection() previewConnection {
	connection := configuredDisplayConnection()
	return previewConnection{connectionContext: connection.connectionContext, AddressRedacted: connection.AddressRedacted}
}

func configuredDisplayConnection() displayConnection {
	connection := effectiveConnectionContext()
	redacted := false
	// Display connection metadata without URL credentials, query, or fragment.
	endpoint, err := url.Parse(connection.APIBase)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		connection.APIBase = "(invalid configured API address)"
		redacted = true
	} else {
		redacted = endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != ""
		endpoint.User, endpoint.RawQuery, endpoint.Fragment = nil, "", ""
		endpoint.ForceQuery = false
		connection.APIBase = endpoint.String()
	}
	return displayConnection{connectionContext: connection, AddressRedacted: redacted}
}

func renderPreviewConnection(connection previewConnection) {
	_, _ = fmt.Fprintf(osStdout, "Configured profile: %s (source=%s)\nConfigured API: %s (source=%s)\nConnection: not verified; credentials and remote access were not checked.\n", oneLine(connection.Profile), oneLine(connection.ProfileSource), oneLine(connection.APIBase), oneLine(connection.APISource))
	if connection.AddressRedacted {
		_, _ = fmt.Fprintln(osStdout, "API display omits credentials, query, or fragment; malformed addresses are hidden.")
	}
}
