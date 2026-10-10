package jailsetup

import (
	"strconv"
	"strings"
)

func deviceFDMountID(data []byte) (uint64, bool) {
	var id uint64
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "mnt_id:" {
			continue
		}
		if id != 0 || len(fields) != 2 {
			return 0, false
		}
		var err error
		id, err = strconv.ParseUint(fields[1], 10, 64)
		if err != nil || id == 0 {
			return 0, false
		}
	}
	return id, id != 0
}

func deviceMountPresent(data []byte, id uint64) bool {
	found := false
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			return false
		}
		actual, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil || actual == 0 {
			return false
		}
		if actual == id {
			found = true
		}
	}
	return found
}

func devicePIDHandleMatches(data []byte, pid int) bool {
	seen := false
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 0 && fields[0] == "Pid:" {
			if seen || len(fields) != 2 {
				return false
			}
			actual, err := strconv.Atoi(fields[1])
			if err != nil || actual != pid {
				return false
			}
			seen = true
		}
	}
	return seen
}

func deviceMountAccess(data []byte, id uint64) bool {
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			return false
		}
		actual, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return false
		}
		if actual != id {
			continue
		}
		flags := make(map[string]bool)
		for _, flag := range strings.Split(fields[5], ",") {
			flags[flag] = true
		}
		return flags["rw"] && !flags["ro"] && !flags["nodev"] && flags["nosuid"] && flags["noexec"]
	}
	return false
}

func snapshotMountAccess(data []byte, id uint64) bool {
	if !deviceMountPresent(data, id) {
		return false
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		actual, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil || actual != id {
			continue
		}
		flags := map[string]bool{}
		for _, flag := range strings.Split(fields[5], ",") {
			flags[flag] = true
		}
		return flags["rw"] && !flags["ro"] && flags["nodev"] && flags["nosuid"] && flags["noexec"]
	}
	return false
}
