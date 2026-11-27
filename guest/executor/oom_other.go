//go:build !linux

package executor

import "os"

func readGuestOOMKills() (uint64, bool)   { return 0, false }
func processKilled(*os.ProcessState) bool { return false }
