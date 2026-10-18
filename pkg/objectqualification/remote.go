//go:build linux

package objectqualification

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type remoteAgent struct {
	command                   *exec.Cmd
	input                     io.WriteCloser
	encoder                   *json.Encoder
	scanner                   *bufio.Scanner
	done                      chan struct{}
	gate                      chan struct{}
	options                   RemoteOptions
	socket, temporary         string
	identity                  AgentIdentity
	source, endpoint, metrics string
}

func (o RemoteOptions) validate() error {
	if o.Host == "" {
		if o.Agent != "" || o.Daemon != "" || o.Control != "" || o.Directory != "" || o.SSHConfig != "" || o.AllowSharedHost {
			return errors.New("remote options require --follower-ssh")
		}
		return nil
	}
	if strings.HasPrefix(o.Host, "-") || strings.ContainsAny(o.Host, " \t\r\n\x00") {
		return errors.New("invalid OpenSSH target")
	}
	for _, path := range []string{o.Agent, o.Daemon, o.Control, o.Directory} {
		if !filepath.IsAbs(path) || strings.ContainsAny(path, "\n\r\x00") {
			return errors.New("remote executable and directory paths must be absolute")
		}
	}
	return nil
}

func kernelBootDigest() (string, error) {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(data)) == "" {
		return "", errors.New("kernel boot identity unavailable")
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

func (r *remoteAgent) sshArgs() []string {
	args := []string{"-T", "-oBatchMode=yes", "-oStrictHostKeyChecking=yes", "-oExitOnForwardFailure=yes", "-oConnectTimeout=" + strconv.Itoa(int(api.ObjectQualificationStartupTimeout/time.Second)), "-S", r.socket}
	if r.options.SSHConfig != "" {
		args = append(args, "-F", r.options.SSHConfig)
	}
	return args
}

func shellArgument(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

func (l *lab) connectRemote(ctx context.Context) (_ *remoteAgent, err error) {
	temporary, err := os.MkdirTemp("", "object-ssh-")
	if err != nil {
		return nil, err
	}
	r := &remoteAgent{options: l.options.Remote, temporary: temporary, socket: filepath.Join(temporary, "socket"), done: make(chan struct{}), gate: make(chan struct{}, 1)}
	defer func() {
		if err != nil {
			r.close(ctx)
		}
	}()
	args := append(r.sshArgs(), "-M", "-oControlPersist=no", r.options.Host, "exec "+shellArgument(r.options.Agent)+" --agent")
	// #nosec G204 -- Fixed OpenSSH options, validated target, shell-quoted executable;
	// all operation data and generated credentials travel only in private stdin.
	r.command = exec.CommandContext(ctx, "ssh", args...)
	r.command.Env = withoutNodeCredentials(os.Environ())
	r.input, err = r.command.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := r.command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	log, err := os.CreateTemp(l.root, "remote-agent-*.log")
	if err != nil {
		return nil, err
	}
	r.command.Stderr = log
	managed, err := startManagedCommand(r.command, func() { _ = log.Close() })
	if err != nil {
		return nil, err
	}
	r.done = managed.done
	r.encoder, r.scanner = json.NewEncoder(r.input), bufio.NewScanner(output)
	r.scanner.Buffer(make([]byte, api.ObjectQualificationAgentChunkBytes), api.ObjectQualificationAgentLineBytes)
	response, err := r.call(ctx, agentRequest{Operation: "init", Init: &agentInit{Directory: r.options.Directory + "-" + l.mode, Daemon: r.options.Daemon, Control: r.options.Control, Key: l.key, Secret: l.secret, Workers: l.options.Workers, WorkBytes: l.options.WorkBytesPerSecond, WorkEntries: l.options.WorkEntriesPerSecond, AutomaticCompaction: l.options.AutomaticCompaction, CompactionRecords: l.options.CompactionRecords}})
	if err != nil {
		return nil, err
	}
	if response.Identity == nil || response.Identity.ProtocolVersion != 2 {
		return nil, errors.New("missing remote qualification identity")
	}
	r.identity = *response.Identity
	local, err := kernelBootDigest()
	if err != nil {
		return nil, err
	}
	if local == r.identity.KernelBootSHA256 && !r.options.AllowSharedHost {
		return nil, errors.New("primary and follower share a kernel boot identity; use distinct hosts or explicit --allow-shared-host for transport validation")
	}
	for path, expected := range map[string]string{l.options.Daemon: r.identity.DaemonSHA256, l.options.Control: r.identity.ControlSHA256} {
		digest, err := binaryDigest(path)
		if err != nil {
			return nil, err
		}
		if digest != expected {
			return nil, errors.New("remote and local node executable hashes differ")
		}
	}
	_, proxyPort, err := net.SplitHostPort(strings.TrimPrefix(l.proxy.server.URL, "http://"))
	if err != nil {
		return nil, err
	}
	port, err := r.forward(ctx, "-R", "127.0.0.1:0:127.0.0.1:"+proxyPort)
	if err != nil {
		return nil, err
	}
	allocated, err := strconv.ParseUint(strings.TrimSpace(port), 10, 16)
	if err != nil || allocated == 0 {
		return nil, errors.New("OpenSSH did not return an allocated reverse-forward port")
	}
	r.source = "http://127.0.0.1:" + strconv.FormatUint(allocated, 10)
	address, metrics, err := loopbackAddresses()
	if err != nil {
		return nil, err
	}
	if _, err = r.forward(ctx, "-L", address+":"+r.identity.Address); err != nil {
		return nil, err
	}
	if _, err = r.forward(ctx, "-L", metrics+":"+r.identity.Metrics); err != nil {
		return nil, err
	}
	r.endpoint, r.metrics = "http://"+address, "http://"+metrics+"/metrics"
	return r, nil
}

func (r *remoteAgent) forward(ctx context.Context, direction, mapping string) (string, error) {
	operation, cancel := context.WithTimeout(ctx, api.ObjectQualificationStartupTimeout)
	defer cancel()
	args := append(r.sshArgs(), "-O", "forward", direction, mapping, r.options.Host)
	// #nosec G204 -- Validated SSH target; forwarding addresses are generated loopback endpoints.
	command := exec.CommandContext(operation, "ssh", args...)
	command.Env = withoutNodeCredentials(os.Environ())
	command.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("qualification SSH forwarding: %w", err)
	}
	return string(output), nil
}

func (r *remoteAgent) call(ctx context.Context, request agentRequest) (agentResponse, error) {
	operation, cancel := context.WithTimeout(ctx, api.ObjectQualificationRemoteTimeout)
	defer cancel()
	select {
	case r.gate <- struct{}{}:
	case <-operation.Done():
		return agentResponse{}, operation.Err()
	}
	defer func() { <-r.gate }()
	type reply struct {
		response agentResponse
		err      error
	}
	result := make(chan reply, 1)
	go func() {
		var response agentResponse
		err := r.encoder.Encode(request)
		if err == nil {
			if r.scanner.Scan() {
				err = json.Unmarshal(r.scanner.Bytes(), &response)
			} else {
				err = errors.Join(io.ErrUnexpectedEOF, r.scanner.Err())
			}
		}
		if err == nil && response.Error != "" {
			err = errors.New(response.Error)
		}
		result <- reply{response, err}
	}()
	select {
	case got := <-result:
		return got.response, got.err
	case <-operation.Done():
		if r.command != nil && r.command.Process != nil {
			_ = r.command.Process.Kill()
		}
		_ = r.input.Close()
		<-result // A canceled stream cannot be reused with ambiguous framing.
		return agentResponse{}, operation.Err()
	}
}

func (r *remoteAgent) close(ctx context.Context) {
	if r == nil {
		return
	}
	if r.command != nil && r.command.Process != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectQualificationStopTimeout)
		if r.encoder != nil {
			_, _ = r.call(cleanup, agentRequest{Operation: "close"})
		}
		cancel()
		if r.input != nil {
			_ = r.input.Close()
		}
		select {
		case <-r.done:
		case <-time.After(api.ObjectQualificationStopTimeout):
			_ = r.command.Process.Kill()
			<-r.done
		}
	}
	_ = os.RemoveAll(r.temporary)
}

func (r *remoteAgent) stop(ctx context.Context, operation string, killed bool) error {
	ctx, cancel := context.WithTimeout(ctx, api.ObjectQualificationStopTimeout)
	defer cancel()
	_, err := r.call(ctx, agentRequest{Operation: operation, Killed: killed})
	return err
}
