package jailsetup

import (
	"errors"
	"io"
)

// AwaitLaunchGate permits the jailer only after vmmd has durably recorded the
// child's kernel incarnation. If vmmd dies before that write, its pipe closes
// and the helper cannot turn an unrecorded child into a running VM.
func AwaitLaunchGate(reader io.Reader) error {
	var token [1]byte
	if _, err := io.ReadFull(reader, token[:]); err != nil {
		return err
	}
	if token[0] != 1 {
		return errors.New("native launch was not authorized")
	}
	return nil
}
