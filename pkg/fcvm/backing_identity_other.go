//go:build !linux

package fcvm

import "os"

// fileBirthTime is not used off Linux; identities fall back to
// (device, inode, size).
func fileBirthTime(*os.File) (sec int64, nsec uint32, ok bool) { return 0, 0, false }

type noBackingDigests struct{}

func defaultBackingDigestRecorder() backingDigestRecorder { return noBackingDigests{} }

func (noBackingDigests) recorded(*os.File, int64) (string, bool) { return "", false }

func (noBackingDigests) record(*os.File, int64, string) error { return nil }
