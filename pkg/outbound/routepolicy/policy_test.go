package routepolicy

import "testing"

func TestCanonicalPathAcceptsCanonicalEscapesAndRejectsAmbiguousSegments(t *testing.T) {
	for _, path := range []string{"/v1/contacts/A%20B", "/v1/caf%C3%A9", "/v1/value%2Bplus"} {
		if !CanonicalPath(path) {
			t.Errorf("CanonicalPath(%q) = false, want true", path)
		}
	}
	for _, path := range []string{"/v1/%2e%2e/admin", "/v1/%2Fadmin", "/v1/%252E%252E", "/v1/%7E", "/v1/%0A", "/v1/%zz", "/v1/%20bad%2fescape"} {
		if CanonicalPath(path) {
			t.Errorf("CanonicalPath(%q) = true, want false", path)
		}
	}
}
