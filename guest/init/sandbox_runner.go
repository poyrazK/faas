package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// sandboxCommandSpec is one command a session starts.
type sandboxCommandSpec struct {
	argv       []string
	env        []string
	dir        string
	root       bool
	tty        bool
	rows, cols uint16
}

// sandboxExit is how a command ended: its exit code, or the signal that
// killed it (the code is then 128 plus the signal, as a shell reports it).
type sandboxExit struct {
	code   int
	signal int
}

func exitFromStatus(ws syscall.WaitStatus) sandboxExit {
	if ws.Signaled() {
		return sandboxExit{code: 128 + int(ws.Signal()), signal: int(ws.Signal())}
	}
	return sandboxExit{code: ws.ExitStatus()}
}

// sandboxProcess is a started command: its process group, its streams and
// its end.
type sandboxProcess interface {
	stdin() io.WriteCloser
	stdout() io.ReadCloser
	// stderr is nil for a command on a terminal, whose output is all on
	// stdout.
	stderr() io.ReadCloser
	wait() sandboxExit
	signal(sig syscall.Signal) error
	resize(rows, cols uint16) error
	// closeOutput closes the output streams once the command has ended, so
	// a reader blocked on output a detached grandchild still holds returns.
	closeOutput()
}

type sandboxRunner interface {
	start(spec sandboxCommandSpec) (sandboxProcess, error)
}

// execRunner starts each command in its own process group (a terminal
// command in its own session), so a stop reaches everything it started.
type execRunner struct {
	// uid is the session user's when guest-init runs as root, and -1 when
	// commands cannot change user (tests).
	uid int
	// reap waits for a pid through PID 1's reaper; nil waits with os/exec.
	reap func(pid int) <-chan sandboxExit
	// openPTY allocates a terminal owned by uid; nil refuses tty commands.
	openPTY func(uid int) (master, slave *os.File, err error)
	// setSize resizes a terminal.
	setSize func(master *os.File, rows, cols uint16) error
}

type execProcess struct {
	cmd     *exec.Cmd
	in      io.WriteCloser
	out     *os.File
	errOut  *os.File
	master  *os.File
	setSize func(*os.File, uint16, uint16) error
	exited  <-chan sandboxExit
}

func (r execRunner) start(spec sandboxCommandSpec) (sandboxProcess, error) {
	cmd := &exec.Cmd{Path: lookCommand(spec.argv[0], spec.env), Args: spec.argv, Env: spec.env, Dir: spec.dir,
		SysProcAttr: &syscall.SysProcAttr{}}
	if !spec.root && r.uid >= 0 {
		cmd.SysProcAttr.Credential = &syscall.Credential{Uid: uint32(r.uid), Gid: uint32(r.uid), Groups: []uint32{}}
	}
	p := &execProcess{cmd: cmd, setSize: r.setSize}
	var childEnds []*os.File
	if spec.tty {
		if r.openPTY == nil {
			return nil, errors.New("terminals are not available")
		}
		uid := r.uid
		if spec.root {
			uid = 0
		}
		master, slave, err := r.openPTY(uid)
		if err != nil {
			return nil, err
		}
		if spec.rows > 0 && spec.cols > 0 && r.setSize != nil {
			_ = r.setSize(master, spec.rows, spec.cols)
		}
		cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
		cmd.SysProcAttr.Setsid, cmd.SysProcAttr.Setctty = true, true
		p.master, p.in, p.out = master, master, master
		childEnds = append(childEnds, slave)
	} else {
		inR, inW, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		outR, outW, err := os.Pipe()
		if err != nil {
			return nil, closeAll(err, inR, inW)
		}
		errR, errW, err := os.Pipe()
		if err != nil {
			return nil, closeAll(err, inR, inW, outR, outW)
		}
		cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, errW
		cmd.SysProcAttr.Setpgid = true
		p.in, p.out, p.errOut = inW, outR, errR
		childEnds = append(childEnds, inR, outW, errW)
	}
	err := cmd.Start()
	for _, f := range childEnds {
		_ = f.Close()
	}
	if err != nil {
		p.closeOutput()
		_ = p.in.Close()
		return nil, err
	}
	if r.reap != nil {
		p.exited = r.reap(cmd.Process.Pid)
	} else {
		exited := make(chan sandboxExit, 1)
		go func() {
			_ = cmd.Wait()
			ws, _ := cmd.ProcessState.Sys().(syscall.WaitStatus)
			exited <- exitFromStatus(ws)
		}()
		p.exited = exited
	}
	return p, nil
}

func closeAll(err error, files ...*os.File) error {
	for _, f := range files {
		_ = f.Close()
	}
	return err
}

func (p *execProcess) stdin() io.WriteCloser { return p.in }
func (p *execProcess) stdout() io.ReadCloser { return p.out }
func (p *execProcess) stderr() io.ReadCloser {
	if p.errOut == nil {
		return nil
	}
	return p.errOut
}
func (p *execProcess) wait() sandboxExit { return <-p.exited }

// signal reaches the whole process group the command leads.
func (p *execProcess) signal(sig syscall.Signal) error {
	err := syscall.Kill(-p.cmd.Process.Pid, sig)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func (p *execProcess) resize(rows, cols uint16) error {
	if p.master == nil || p.setSize == nil {
		return errors.New("the command has no terminal")
	}
	return p.setSize(p.master, rows, cols)
}

func (p *execProcess) closeOutput() {
	_ = p.out.Close()
	if p.errOut != nil {
		_ = p.errOut.Close()
	}
}

// lookCommand resolves a bare command name on the command's own PATH; a
// name with a slash is used as given, relative to the command's directory.
func lookCommand(name string, env []string) string {
	if strings.Contains(name, "/") {
		return name
	}
	for _, kv := range env {
		dirs, ok := strings.CutPrefix(kv, "PATH=")
		if !ok {
			continue
		}
		for _, dir := range filepath.SplitList(dirs) {
			candidate := filepath.Join(dir, name)
			//nolint:forbidigo // a guest command lookup on the guest's own PATH, not a customer workspace path
			if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
				return candidate
			}
		}
	}
	return name
}

// sandboxEnv is a command's environment: a fixed base for its user, then the
// request's variables, then the platform's relay URL.
func sandboxEnv(home, user string, tty bool, requested map[string]string) []string {
	env := map[string]string{
		"PATH":   "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME":   home,
		"USER":   user,
		"LANG":   "C.UTF-8",
		"TMPDIR": "/tmp",
	}
	if tty {
		env["TERM"] = "xterm-256color"
	}
	for k, v := range requested {
		env[k] = v
	}
	env["GREGALE_OUTBOUND_URL"] = OutboundRelayEndpoint
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, fmt.Sprintf("%s=%s", k, v))
	}
	return out
}
