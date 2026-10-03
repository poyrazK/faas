//go:build !unix

package main

import (
	"errors"
	"os"
	"os/exec"
)

func configureLocalAppProcess(*exec.Cmd) error {
	return errors.New("managed local apps require a Unix platform; start the app separately and use --base-url")
}

func signalLocalAppProcess(*exec.Cmd, bool) error {
	return errors.New("managed local apps are unsupported on this platform")
}

func localAppProcessGroupAlive(*exec.Cmd) bool { return false }

func testTerminationSignals() []os.Signal { return []os.Signal{os.Interrupt} }
