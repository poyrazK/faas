// adr: 20261018170742 — access kinds in request logs, never subjects, addresses or keys.
package main

import "errors"

type dataAPIAccessRecord struct {
	Kind                string `json:"kind"`
	ServerKeySlot       string `json:"server_key_slot,omitempty"`
	ServerKeyRestricted bool   `json:"server_key_restricted,omitempty"`
}

// Observed access kinds; older runtimes and pre-classification rejections have none.
type dataAPIAccessStats struct {
	User      int `json:"user"`
	Anonymous int `json:"anonymous"`
	Server    int `json:"server"`
}

func validateDataAPIAccess(record dataAPIRequestRecord) error {
	a := record.Access
	if a == nil {
		return nil
	}
	if !dataAPIDiagnosticAllowed(a.Kind, "user", "anonymous", "server") || !dataAPIDiagnosticAllowed(record.Route, "rest", "rpc", "schema", "openapi") {
		return errors.New("invalid access metadata")
	}
	if (a.Kind == "server") != (a.ServerKeySlot != "") || (a.ServerKeySlot != "" && !dataAPIDiagnosticAllowed(a.ServerKeySlot, "a", "b")) || (a.ServerKeyRestricted && a.Kind != "server") {
		return errors.New("inconsistent server key metadata")
	}
	if a.Kind == "anonymous" && (record.Route != "rest" || !dataAPIDiagnosticAllowed(record.Method, "GET", "HEAD")) {
		return errors.New("inconsistent anonymous access metadata")
	}
	return nil
}

func (s *dataAPIAccessStats) add(a *dataAPIAccessRecord) {
	if a == nil {
		return
	}
	switch a.Kind {
	case "user":
		s.User++
	case "anonymous":
		s.Anonymous++
	case "server":
		s.Server++
	}
}
