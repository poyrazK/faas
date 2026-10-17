//go:build !linux

package promotionrollout

import "os"

func openReadOnly(path string) (*os.File, error) { return os.Open(path) }
