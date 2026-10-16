//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

func testCampaignProcessDomain() (string, string, error) {
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", "", err
	}
	self, err := os.Readlink("/proc/self")
	if err != nil || self != strconv.Itoa(os.Getpid()) {
		return "", "", errors.New("procfs does not expose the current PID namespace")
	}
	namespace, err := os.Readlink("/proc/self/ns/pid")
	if err != nil {
		return "", "", err
	}
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return "", "", err
	}
	verifiedNamespace := false
	for _, line := range strings.Split(string(status), "\n") {
		if strings.HasPrefix(line, "NSpid:") {
			fields := strings.Fields(line)
			verifiedNamespace = len(fields) == 2 && fields[1] == strconv.Itoa(os.Getpid())
		}
	}
	if !verifiedNamespace {
		return "", "", errors.New("procfs PID namespace cannot be verified")
	}
	id := strings.TrimSpace(string(boot))
	if len(id) != 36 || !strings.HasPrefix(namespace, "pid:[") {
		return "", "", errors.New("unverifiable local process domain")
	}
	return id, namespace, nil
}

func readTestCampaignProcessStat(pid int) (string, string, error) {
	if pid <= 0 {
		return "", "", errors.New("invalid PID")
	}
	body, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", "", err
	}
	text := string(body)
	end := strings.LastIndex(text, ")")
	if end < 0 || !strings.HasPrefix(text, strconv.Itoa(pid)+" (") {
		return "", "", errors.New("invalid process stat")
	}
	fields := strings.Fields(text[end+1:])
	if len(fields) <= 19 || len(fields[0]) != 1 {
		return "", "", errors.New("incomplete process stat")
	}
	start, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || start == 0 {
		return "", "", errors.New("invalid process start identity")
	}
	return fields[19], fields[0], nil
}

func readTestCampaignProcessIdentity(pid int) (testCampaignProcessIdentity, error) {
	identity := testCampaignProcessIdentity{PID: pid}
	boot, namespace, err := testCampaignProcessDomain()
	if err != nil {
		return identity, err
	}
	start, _, err := readTestCampaignProcessStat(pid)
	if err != nil {
		return identity, err
	}
	identity.BootID, identity.Namespace, identity.Start = boot, namespace, start
	return identity, nil
}

// procfs returns ESRCH when a task is reaped after its stat file was opened;
// a lookup after reaping returns ENOENT. Both prove absence in the verified PID
// domain. Permission, I/O and parsing errors remain unverifiable.
func testCampaignProcessGone(err error) bool {
	return os.IsNotExist(err) || errors.Is(err, syscall.ESRCH)
}

func probeTestCampaignProcess(identity testCampaignProcessIdentity) (string, string) {
	boot, namespace, err := testCampaignProcessDomain()
	start, parseErr := strconv.ParseUint(identity.Start, 10, 64)
	if err != nil || identity.PID <= 0 || parseErr != nil || start == 0 ||
		identity.BootID != boot || identity.Namespace != namespace {
		return "unverifiable", "process domain or start identity cannot be verified on this host"
	}
	current, state, err := readTestCampaignProcessStat(identity.PID)
	if testCampaignProcessGone(err) {
		return "terminated", "recorded PID no longer exists in the original process domain"
	}
	if err != nil {
		return "unverifiable", "process lookup failed"
	}
	if current != identity.Start {
		return "terminated", "PID was reused; the recorded process has terminated"
	}
	switch state {
	case "Z", "X", "x":
		return "terminated", "recorded process exited"
	case "T", "t":
		return "stopped", "stopped process can resume"
	default:
		return "alive", "recorded process is still active"
	}
}
