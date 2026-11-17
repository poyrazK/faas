package main

// Debug sessions (ADR-20261117070620): `gregale app exec --debug` runs a
// command in a task VM with the runtime base's static busybox mounted at
// api.AppTaskDebugToolsDir, only in that session's private mount namespace.
// guest-init re-executes itself as a helper that attaches the tools, drops to
// the deployment user and execs the command. This file holds the portable
// parts: the helper's argument encoding and the session PATH.

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	// appTaskDebugHelperArg0 selects the debug helper in a re-executed
	// guest-init child.
	appTaskDebugHelperArg0 = "guest-init-debug"
	// appTaskDebugBaseToolsDir is where every runtime base ships the tools:
	// a static busybox (root:root 0111) and bin/<applet> symlinks to it.
	appTaskDebugBaseToolsDir = "/usr/lib/gregale/debug"
	// appTaskDebugToolsFD is the helper's fd for the detached tools mount
	// (exec.Cmd.ExtraFiles[0]).
	appTaskDebugToolsFD = 3
)

// appTaskDebugIdentity is the deployment user the helper drops to.
type appTaskDebugIdentity struct {
	UID, GID uint32
	Groups   []int
}

// encodeAppTaskDebugHelperArgs is the helper's argv:
// arg0 uid gid groups(comma-separated) -- command...
func encodeAppTaskDebugHelperArgs(id appTaskDebugIdentity, command []string) []string {
	groups := make([]string, 0, len(id.Groups))
	for _, g := range id.Groups {
		groups = append(groups, strconv.Itoa(g))
	}
	args := []string{appTaskDebugHelperArg0, strconv.FormatUint(uint64(id.UID), 10), strconv.FormatUint(uint64(id.GID), 10), strings.Join(groups, ","), "--"}
	return append(args, command...)
}

// decodeAppTaskDebugHelperArgs reverses encodeAppTaskDebugHelperArgs for
// os.Args[1:].
func decodeAppTaskDebugHelperArgs(args []string) (appTaskDebugIdentity, []string, error) {
	if len(args) < 5 || args[3] != "--" {
		return appTaskDebugIdentity{}, nil, errors.New("debug helper: malformed arguments")
	}
	uid, err := strconv.ParseUint(args[0], 10, 32)
	if err != nil {
		return appTaskDebugIdentity{}, nil, fmt.Errorf("debug helper uid: %w", err)
	}
	gid, err := strconv.ParseUint(args[1], 10, 32)
	if err != nil {
		return appTaskDebugIdentity{}, nil, fmt.Errorf("debug helper gid: %w", err)
	}
	id := appTaskDebugIdentity{UID: uint32(uid), GID: uint32(gid)}
	if args[2] != "" {
		for _, raw := range strings.Split(args[2], ",") {
			g, err := strconv.ParseUint(raw, 10, 32)
			if err != nil {
				return appTaskDebugIdentity{}, nil, fmt.Errorf("debug helper group: %w", err)
			}
			id.Groups = append(id.Groups, int(g))
		}
	}
	command := args[4:]
	if strings.TrimSpace(command[0]) == "" {
		return appTaskDebugIdentity{}, nil, errors.New("debug helper: empty command")
	}
	return id, command, nil
}

// appendAppTaskDebugPath appends the tools to PATH, so the image's own tools
// win where it has them. env already carries PATH (withDefaultPath).
func appendAppTaskDebugPath(env []string) []string {
	bin := api.AppTaskDebugToolsDir + "/bin"
	out := make([]string, 0, len(env)+1)
	found := false
	for _, kv := range env {
		if value, ok := strings.CutPrefix(kv, "PATH="); ok && !found {
			found = true
			if value == "" {
				kv = "PATH=" + bin
			} else {
				kv = "PATH=" + value + ":" + bin
			}
		}
		out = append(out, kv)
	}
	if !found {
		out = append(out, "PATH="+defaultImagePath+":"+bin)
	}
	return out
}
