package objectqualification

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type inspectionOutput struct {
	buffer   bytes.Buffer
	overflow bool
}

func (b *inspectionOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := api.ObjectQualificationAgentLineBytes - b.buffer.Len()
	if n > remaining {
		b.overflow = true
		p = p[:remaining]
	}
	_, _ = b.buffer.Write(p)
	return n, nil // Drain excess output without retaining it or blocking cleanup.
}

func inspectionSSHArgs(o RemoteOptions) []string {
	args := []string{"-T", "-oBatchMode=yes", "-oStrictHostKeyChecking=yes", "-oClearAllForwardings=yes", "-oPermitLocalCommand=no", "-oControlMaster=no", "-oControlPath=none", "-oRemoteCommand=none", "-oConnectTimeout=" + strconv.Itoa(int(api.ObjectQualificationStartupTimeout/time.Second))}
	if o.SSHConfig != "" {
		args = append(args, "-F", o.SSHConfig)
	}
	return append(args, o.Host, "exec "+shellArgument(o.Agent)+" --inspect")
}

func inspectRemote(ctx context.Context, o RemoteOptions, request inspectionRequest) (host HostInspection, err error) {
	if err = o.validate(); err != nil || o.Host == "" {
		return host, errors.New("qualification preflight requires a configured SSH follower")
	}
	body, err := json.Marshal(request)
	if err != nil {
		return host, err
	}
	if len(body) > api.ObjectQualificationAgentLineBytes {
		return host, errors.New("qualification inspection exceeds its message budget")
	}
	operation, cancel := context.WithTimeout(ctx, api.ObjectQualificationStartupTimeout)
	defer cancel()
	// #nosec G204 -- Validated OpenSSH target; only the helper executable is
	// shell-quoted argv. Inspection paths travel as bounded JSON on stdin.
	command := exec.CommandContext(operation, "ssh", inspectionSSHArgs(o)...)
	command.Env = withoutNodeCredentials(os.Environ())
	command.Stdin = bytes.NewReader(append(body, '\n'))
	var output inspectionOutput
	command.Stdout, command.Stderr = &output, io.Discard
	command.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err = command.Run(); err != nil {
		if operation.Err() != nil {
			return host, operation.Err()
		}
		return host, fmt.Errorf("qualification inspection SSH failed: %w", err)
	}
	if output.overflow {
		return host, errors.New("qualification inspection response exceeds its message budget")
	}
	if err = decodeInspection(bytes.NewReader(output.buffer.Bytes()), &host); err != nil {
		return host, err
	}
	return host, validateHostInspection(host, request)
}
