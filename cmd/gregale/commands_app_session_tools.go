package main

// `gregale app <slug> cp` and `gregale app <slug> port-forward` (ADR-958):
// both run a guest-init built-in in a fresh task VM over a non-tty
// interactive session, using the session's stdin/stdout as byte streams.

import (
	"archive/tar"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskmux"
)

const appSessionStdinChunkBytes = 60 * 1024

// appTaskStreamSession exposes one attached non-tty session as byte streams.
type appTaskStreamSession struct {
	conn     *websocket.Conn
	writeMu  sync.Mutex
	stdout   *io.PipeReader
	terminal chan api.AppTaskAttachTerminalMessage
	readErr  chan error
}

func newAppTaskStreamSession(conn *websocket.Conn, stderr io.Writer) *appTaskStreamSession {
	stdoutR, stdoutW := io.Pipe()
	s := &appTaskStreamSession{
		conn: conn, stdout: stdoutR,
		terminal: make(chan api.AppTaskAttachTerminalMessage, 1), readErr: make(chan error, 1),
	}
	go func() {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				_ = stdoutW.CloseWithError(err)
				s.readErr <- err
				return
			}
			if len(msg) == 0 {
				continue
			}
			switch msg[0] {
			case api.AppTaskAttachStdout:
				if _, err := stdoutW.Write(msg[1:]); err != nil {
					s.readErr <- err
					return
				}
			case api.AppTaskAttachStderr:
				_, _ = stderr.Write(msg[1:])
			case api.AppTaskAttachTerminal:
				_ = stdoutW.Close()
				terminal, err := api.DecodeAppTaskAttachTerminal(msg[1:])
				if err != nil {
					s.readErr <- err
					return
				}
				s.terminal <- terminal
				return
			}
		}
	}()
	return s
}

// Write sends p as session stdin.
func (s *appTaskStreamSession) Write(p []byte) (int, error) {
	written := 0
	for written < len(p) {
		n := min(len(p)-written, appSessionStdinChunkBytes)
		s.writeMu.Lock()
		err := s.conn.WriteMessage(websocket.BinaryMessage, append([]byte{api.AppTaskAttachStdin}, p[written:written+n]...))
		s.writeMu.Unlock()
		if err != nil {
			return written, err
		}
		written += n
	}
	return written, nil
}

// CloseStdin tells the remote process its stdin has ended.
func (s *appTaskStreamSession) CloseStdin() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.WriteMessage(websocket.BinaryMessage, []byte{api.AppTaskAttachStdinClose})
}

// Wait returns the session's terminal message.
func (s *appTaskStreamSession) Wait(ctx context.Context) (api.AppTaskAttachTerminalMessage, error) {
	select {
	case terminal := <-s.terminal:
		return terminal, nil
	case err := <-s.readErr:
		return api.AppTaskAttachTerminalMessage{}, err
	case <-ctx.Done():
		return api.AppTaskAttachTerminalMessage{}, ctx.Err()
	}
}

// openAppTaskStreamSession admits a non-tty interactive task running command
// and attaches to it.
func openAppTaskStreamSession(ctx context.Context, client *api.Client, slug string, command []string, waitTimeout time.Duration) (*appTaskStreamSession, func(), error) {
	request := api.CreateAppTaskRequest{Command: command, Interactive: true}
	if _, problem := request.Resolve(); problem != nil {
		return nil, nil, problem
	}
	task, err := client.CreateAppTask(ctx, slug, request)
	if err != nil {
		return nil, nil, err
	}
	if task.Attach == nil || task.Attach.Token == "" {
		return nil, nil, errors.New("the server did not return attach credentials")
	}
	waitCtx, cancel := context.WithTimeout(ctx, waitTimeout)
	defer cancel()
	conn, err := dialAppTaskAttach(waitCtx, client, slug, task, false, nil)
	if err != nil {
		cancelInteractiveTask(client, slug, task.ID)
		return nil, nil, err
	}
	return newAppTaskStreamSession(conn, osStderr), func() { _ = conn.Close() }, nil
}

func cmdAppCopy(slug string, args []string) int {
	fs := newFlagSet("app-cp", flag.ContinueOnError)
	waitTimeout := fs.Duration("wait-timeout", appExecInteractiveStartupWait, "maximum time to wait for the task VM to start")
	flags, positionals := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil || slug == "" || len(positionals) != 2 {
		PrintUsage(osStderr, "usage: gregale app <slug> cp [--wait-timeout D] <remote-path> <local-path|->", "apps")
		return 1
	}
	remote, local := positionals[0], positionals[1]
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	session, closeSession, err := openAppTaskStreamSession(ctx, client, slug, []string{api.AppTaskCopyOutCommand, remote}, *waitTimeout)
	if err != nil {
		return printErr("Could not start the copy", err)
	}
	defer closeSession()
	_ = session.CloseStdin()
	var copyErr error
	if local == "-" {
		_, copyErr = io.Copy(osStdout, session.stdout)
	} else {
		copyErr = extractAppTaskTar(session.stdout, local)
	}
	if copyErr != nil {
		closeSession()
		return printErr("Copy failed", copyErr)
	}
	terminal, err := session.Wait(ctx)
	if err != nil {
		return printErr("Copy session ended unexpectedly", err)
	}
	return appSessionExitCode(terminal)
}

func appSessionExitCode(terminal api.AppTaskAttachTerminalMessage) int {
	if terminal.ExitCode != nil {
		return *terminal.ExitCode
	}
	if terminal.Failure != nil {
		PrintFail(osStderr, "Session ended: %s (%s)", terminal.Failure.Message, terminal.Failure.Code)
		return appExecInteractiveExitFail
	}
	return 0
}

// extractAppTaskTar writes the archive under dest with docker cp semantics:
// into dest when it is an existing directory, otherwise as dest itself.
// Every write goes through os.Root, so entries cannot escape the target.
func extractAppTaskTar(r io.Reader, dest string) error {
	rootDir, rename := dest, ""
	info, err := os.Stat(dest)
	switch {
	case err == nil && info.IsDir():
	case err == nil || errors.Is(err, fs.ErrNotExist):
		rootDir, rename = filepath.Dir(dest), filepath.Base(dest)
	default:
		return err
	}
	root, err := os.OpenRoot(rootDir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	tr := tar.NewReader(r)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name, err := appTaskTarEntryName(header.Name, rename)
		if err != nil {
			return err
		}
		if err := extractAppTaskTarEntry(root, tr, header, name); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
}

func appTaskTarEntryName(raw, rename string) (string, error) {
	name := path.Clean(strings.TrimSuffix(raw, "/"))
	if name == "." || path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
		return "", fmt.Errorf("refusing archive entry %q", raw)
	}
	if rename != "" {
		if _, rest, found := strings.Cut(name, "/"); found {
			name = rename + "/" + rest
		} else {
			name = rename
		}
	}
	return filepath.FromSlash(name), nil
}

func extractAppTaskTarEntry(root *os.Root, tr *tar.Reader, header *tar.Header, name string) error {
	mode := header.FileInfo().Mode().Perm()
	switch header.Typeflag {
	case tar.TypeDir:
		return root.MkdirAll(name, mode|0o700)
	case tar.TypeReg:
		if dir := filepath.Dir(name); dir != "." {
			if err := root.MkdirAll(dir, 0o755); err != nil {
				return err
			}
		}
		file, err := root.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		if _, err := io.Copy(file, tr); err != nil {
			_ = file.Close()
			return err
		}
		return file.Close()
	case tar.TypeSymlink:
		_ = root.Remove(name)
		return root.Symlink(header.Linkname, name)
	default:
		return nil
	}
}

func cmdAppPortForward(slug string, args []string) int {
	fs := newFlagSet("app-port-forward", flag.ContinueOnError)
	address := fs.String("address", "127.0.0.1", "local address to listen on")
	waitTimeout := fs.Duration("wait-timeout", appExecInteractiveStartupWait, "maximum time to wait for the task VM to start")
	flags, positionals := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil || slug == "" || len(positionals) != 1 {
		PrintUsage(osStderr, "usage: gregale app <slug> port-forward [--address A] [--wait-timeout D] [LOCAL_PORT:]HOST:PORT", "apps")
		return 1
	}
	localPort, target, err := parsePortForwardSpec(positionals[0])
	if err != nil {
		return printErr("Invalid forward", err)
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(*address, strconv.Itoa(localPort)))
	if err != nil {
		return printErr("Could not listen locally", err)
	}
	defer func() { _ = ln.Close() }()
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	session, closeSession, err := openAppTaskStreamSession(ctx, client, slug, []string{api.AppTaskPortForwardCommand, target}, *waitTimeout)
	if err != nil {
		return printErr("Could not start forwarding", err)
	}
	defer closeSession()
	PrintOK(osStderr, "Forwarding %s -> %s from inside %s (Ctrl-C to stop)", ln.Addr(), target, slug)
	forwardErr := apptaskmux.Forward(ctx, ln, session, session.stdout)
	_ = session.CloseStdin()
	waitCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	terminal, waitErr := session.Wait(waitCtx)
	if ctx.Err() != nil {
		return 0
	}
	if forwardErr != nil && waitErr != nil {
		return printErr("Forwarding ended unexpectedly", forwardErr)
	}
	return appSessionExitCode(terminal)
}

// parsePortForwardSpec accepts HOST:PORT (listening on the same port) or
// LOCAL_PORT:HOST:PORT; LOCAL_PORT 0 picks a free port.
func parsePortForwardSpec(spec string) (int, string, error) {
	if api.ValidPortForwardTarget(spec) {
		_, port, _ := net.SplitHostPort(spec)
		value, _ := strconv.Atoi(port)
		return value, spec, nil
	}
	local, target, found := strings.Cut(spec, ":")
	if !found || !api.ValidPortForwardTarget(target) {
		return 0, "", fmt.Errorf("%q is not [LOCAL_PORT:]HOST:PORT", spec)
	}
	value, err := strconv.Atoi(local)
	if err != nil || value < 0 || value > 65535 {
		return 0, "", fmt.Errorf("local port %q is outside 0..65535", local)
	}
	return value, target, nil
}
