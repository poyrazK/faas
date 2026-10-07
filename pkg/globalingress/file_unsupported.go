//go:build !linux && !darwin

package globalingress

// Operator handoff paths currently support Linux and macOS only.
func ReadManifest(string) ([]byte, error) { return nil, ErrUnavailable }
