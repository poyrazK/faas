package imaged

import "os"

// writeFileImpl is a test-only thin wrapper used by handler_image_build_test.go.
// It exists so the test file doesn't have to import os directly.
func writeFileImpl(path string, data []byte, mode uint32) error {
	return os.WriteFile(path, data, os.FileMode(mode))
}
