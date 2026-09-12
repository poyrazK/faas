package main

import "fmt"

// validateCLILimit enforces the bounds advertised by a paginated command.
// Keeping this check at the CLI boundary prevents zero, negative, and
// oversized values from being silently replaced by API defaults.
func validateCLILimit(name string, value, max int) error {
	if value < 1 || value > max {
		return fmt.Errorf("--%s must be between 1 and %d (got %d)", name, max, value)
	}
	return nil
}

// validateCLIOffset rejects negative cursors before a request is sent.
func validateCLIOffset(name string, value int) error {
	if value < 0 {
		return fmt.Errorf("--%s must be non-negative (got %d)", name, value)
	}
	return nil
}
