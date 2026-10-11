//go:build !unix

package main

import "os/exec"

// CommandContext terminates the direct process on platforms without the Unix
// process-group support used for deployment tools and their descendants.
func configureDataAPISyncProcess(*exec.Cmd) {}

func cleanupDataAPISyncProcess(*exec.Cmd) {}
