package main

// Build-tag-free half of the Docker-compatible process environment every
// guest gives its workloads (ADR-20261025111339): the hostname and the
// /etc/hosts entries, and the passwd home a workload starts in. Mounts and
// file writes live in runtime_env_linux.go.

import (
	"io/fs"
	"strings"
)

const (
	// guestHostname names a guest whose kernel was given no hostname. The
	// kernel's "(none)" resolves nowhere, so Java's getLocalHost and
	// Erlang node names failed. Every guest of an app shares the name, as
	// snapshot-restored guests share every other kernel state.
	guestHostname = "gregale"
	// guestAddress is the guest's address in its private network (ADR-009);
	// the guest hostname resolves to it, as a container's does under Docker.
	guestAddress = "10.0.0.2"
)

// hostsWithGuestEntries returns hosts, the contents of an image's
// /etc/hosts, with the entries a container runtime provides added where the
// image lacks them: localhost on 127.0.0.1 and hostname on the guest address.
// Existing lines are kept, so entries an image bakes in still resolve. No
// ::1 entry is added: a client that tries ::1 first would miss a server
// listening on 0.0.0.0. The bool reports whether anything was added.
func hostsWithGuestEntries(hosts, hostname string) (string, bool) {
	haveLocalhost, haveHostname := false, false
	for _, line := range strings.Split(hosts, "\n") {
		line, _, _ = strings.Cut(line, "#")
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		for _, name := range fields[1:] {
			if strings.EqualFold(name, "localhost") && strings.HasPrefix(fields[0], "127.") {
				haveLocalhost = true
			}
			if strings.EqualFold(name, hostname) {
				haveHostname = true
			}
		}
	}
	var add strings.Builder
	if !haveLocalhost {
		add.WriteString("127.0.0.1\tlocalhost\n")
	}
	if !haveHostname && hostname != "" {
		add.WriteString(guestAddress + "\t" + hostname + "\n")
	}
	if add.Len() == 0 {
		return hosts, false
	}
	if hosts != "" && !strings.HasSuffix(hosts, "\n") {
		hosts += "\n"
	}
	return hosts + add.String(), true
}

// homeWritable reports whether a directory with info's mode and owner is
// one uid:gid can create files in, by its permission bits. Supplementary
// groups are not considered.
func homeWritable(info fs.FileInfo, ownerUID, ownerGID, uid, gid uint32) bool {
	if !info.IsDir() {
		return false
	}
	perm := info.Mode().Perm()
	switch {
	case uid == 0:
		return true
	case ownerUID == uid:
		return perm&0o300 == 0o300
	case ownerGID == gid:
		return perm&0o030 == 0o030
	default:
		return perm&0o003 == 0o003
	}
}

// withDefaultPath gives env Docker's PATH when the image and the deployment
// set none, so the workload's own children resolve commands as under Docker.
func withDefaultPath(env []string) []string {
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") && kv != "PATH=" {
			return env
		}
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if kv != "PATH=" {
			out = append(out, kv)
		}
	}
	return append(out, "PATH="+defaultImagePath)
}
