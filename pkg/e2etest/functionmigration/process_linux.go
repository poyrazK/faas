package functionmigration

// adr: 1548

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

type coordinatorMessage struct {
	event CoordinatorEvent
	err   error
}

// Coordinator owns only a fixture child. Events use inherited fd 3; stdout and
// stderr stay in a private log rather than entering the parent's Go test stream.
type Coordinator struct {
	Identity CoordinatorIdentity
	cmd      *exec.Cmd
	input    io.WriteCloser
	events   chan coordinatorMessage
	done     chan struct{}
	waitErr  error
}

func StartCoordinator(ctx context.Context, binary string, args, env []string, logPath string) (*Coordinator, error) {
	log, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, errors.New("create private coordinator log")
	}
	defer func() { _ = log.Close() }()
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer func() { _ = writer.Close() }()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env, cmd.Stdout, cmd.Stderr, cmd.ExtraFiles = env, log, log, []*os.File{writer}
	input, err := cmd.StdinPipe()
	if err != nil {
		_ = reader.Close()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		_ = reader.Close()
		_ = input.Close()
		return nil, errors.New("start fixture coordinator")
	}
	p := &Coordinator{cmd: cmd, input: input, events: make(chan coordinatorMessage, 4), done: make(chan struct{})}
	go func() { p.waitErr = cmd.Wait(); close(p.done) }()
	p.Identity, err = observeCoordinator(cmd.Process.Pid)
	if err != nil {
		_ = p.Close()
		_ = reader.Close()
		return nil, err
	}
	go func() {
		defer func() { _ = reader.Close() }()
		defer close(p.events)
		decoder := json.NewDecoder(io.LimitReader(reader, 65536))
		for {
			var event CoordinatorEvent
			err := decoder.Decode(&event)
			if err != nil {
				p.events <- coordinatorMessage{err: err}
				return
			}
			p.events <- coordinatorMessage{event: event}
		}
	}()
	return p, nil
}

func observeCoordinator(pid int) (CoordinatorIdentity, error) {
	var result CoordinatorIdentity
	if pid <= 0 {
		return result, errors.New("invalid owned coordinator PID")
	}
	base := "/proc/" + strconv.Itoa(pid)
	raw, err := os.ReadFile(base + "/stat")
	if err != nil {
		return result, errors.New("observe coordinator process generation")
	}
	fields := strings.Fields(string(raw)[strings.LastIndexByte(string(raw), ')')+1:])
	if len(fields) < 20 || !processStart(fields[19]) {
		return result, errors.New("invalid coordinator process generation")
	}
	// StartCoordinator supplies its own child's PID. Read the kernel's loaded
	// executable witness; following this procfs link is intentional and cannot
	// be replaced with the customer-file symlink refusal helper.
	exe, err := os.Open(base + "/exe") //nolint:forbidigo // Vetted owned-child PID, not a customer pathname.
	if err != nil {
		return result, errors.New("observe coordinator executable")
	}
	defer func() { _ = exe.Close() }()
	hash := sha256.New()
	if _, err = io.Copy(hash, exe); err != nil {
		return result, err
	}
	return CoordinatorIdentity{PID: pid, Start: fields[19], BinarySHA: hex.EncodeToString(hash.Sum(nil))}, nil
}

func (p *Coordinator) Done() <-chan struct{} { return p.done }
func (p *Coordinator) Event(ctx context.Context, phase string) (CoordinatorEvent, error) {
	select {
	case message, ok := <-p.events:
		if !ok || message.err != nil || message.event.Phase != phase {
			return CoordinatorEvent{}, errors.New("coordinator missed its required boundary")
		}
		return message.event, nil
	case <-ctx.Done():
		return CoordinatorEvent{}, ctx.Err()
	}
}
func (p *Coordinator) Release() error { _, err := io.WriteString(p.input, "continue\n"); return err }
func (p *Coordinator) Wait(ctx context.Context) error {
	select {
	case <-p.done:
		if p.waitErr != nil {
			return errors.New("fixture coordinator failed")
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (p *Coordinator) Kill(ctx context.Context) error {
	if err := p.cmd.Process.Kill(); err != nil {
		return errors.New("fixture coordinator was not alive at crash boundary")
	}
	select {
	case <-p.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	status, ok := p.cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		return errors.New("coordinator did not terminate through SIGKILL")
	}
	return nil
}
func (p *Coordinator) Close() error {
	// exec.Cmd.Wait also closes StdinPipe; a repeated cleanup close is harmless.
	defer func() { _ = p.input.Close() }()
	select {
	case <-p.done:
		return nil
	default:
	}
	_ = p.cmd.Process.Kill()
	<-p.done
	return nil
}
