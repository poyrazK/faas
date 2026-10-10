package main

// Interactive app commands (ADR-958): `gregale app <slug> exec -it -- sh`
// queues an interactive task, waits for its VM, and attaches the local
// terminal to the remote process through the gateway's WebSocket endpoint.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/term"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	appExecAttachPollInterval  = 250 * time.Millisecond
	appExecAttachHandshake     = 15 * time.Second
	appExecStdinChunkBytes     = 32 * 1024
	appExecDisconnectExitCode  = 255
	appExecInteractiveExitFail = 1
)

// expandShortExecFlags rewrites docker-style short flags (-i, -t, -it, -ti)
// into their long forms; the CLI's argument splitter only recognises long
// flags. Arguments after "--" are untouched.
func expandShortExecFlags(args []string) []string {
	out := make([]string, 0, len(args)+1)
	for i, arg := range args {
		if arg == "--" {
			return append(out, args[i:]...)
		}
		switch arg {
		case "-i":
			out = append(out, "--interactive")
		case "-t":
			out = append(out, "--tty")
		case "-it", "-ti":
			out = append(out, "--interactive", "--tty")
		default:
			out = append(out, arg)
		}
	}
	return out
}

type interactiveAppExec struct {
	client      *api.Client
	slug        string
	request     api.CreateAppTaskRequest
	waitTimeout time.Duration
}

func runInteractiveAppExec(ctx context.Context, exec interactiveAppExec) int {
	stdinFile, _ := osStdin.(*os.File)
	stdinIsTTY := stdinFile != nil && term.IsTerminal(int(stdinFile.Fd()))
	if exec.request.TTY && !stdinIsTTY {
		PrintWarn(osStderr, "stdin is not a terminal; continuing without a remote tty")
		exec.request.TTY = false
	}
	task, err := exec.client.CreateAppTask(ctx, exec.slug, exec.request)
	if err != nil {
		return printErr("Interactive session submission failed", err)
	}
	if task.Attach == nil || task.Attach.Token == "" {
		return printErr("Interactive session unavailable", errors.New("the server did not return attach credentials"))
	}
	waitCtx, cancel := context.WithTimeout(ctx, exec.waitTimeout)
	defer cancel()
	conn, err := dialAppTaskAttach(waitCtx, exec.client, exec.slug, task, exec.request.TTY, stdinFile)
	if err != nil {
		if errors.Is(waitCtx.Err(), context.Canceled) {
			cancelInteractiveTask(exec.client, exec.slug, task.ID)
			return 130
		}
		return printErr("Could not attach to the session", err)
	}
	defer func() { _ = conn.Close() }()

	restore := func() {}
	if exec.request.TTY {
		state, rawErr := term.MakeRaw(int(stdinFile.Fd()))
		if rawErr != nil {
			return printErr("Could not put the terminal in raw mode", rawErr)
		}
		var once sync.Once
		restore = func() { once.Do(func() { _ = term.Restore(int(stdinFile.Fd()), state) }) }
	}
	terminal, sessionErr := pumpAppTaskAttach(ctx, conn, exec.request.TTY, stdinFile)
	restore()
	if errors.Is(sessionErr, context.Canceled) {
		// Disconnecting ends the remote session; the server records it.
		return 130
	}
	if sessionErr != nil {
		PrintFail(osStderr, "Session ended unexpectedly: %v", sessionErr)
		return appExecDisconnectExitCode
	}
	if terminal.Failure != nil && terminal.ExitCode == nil {
		PrintFail(osStderr, "Session ended: %s (%s)", terminal.Failure.Message, terminal.Failure.Code)
		return appExecInteractiveExitFail
	}
	if terminal.ExitCode != nil {
		return *terminal.ExitCode
	}
	return 0
}

// dialAppTaskAttach waits for the task VM and opens the attach WebSocket.
// 409 means the VM is still starting; anything else is final.
func dialAppTaskAttach(ctx context.Context, client *api.Client, slug string, task api.AppTaskResponse, tty bool, stdin *os.File) (*websocket.Conn, error) {
	base, err := url.Parse(client.BaseURL())
	if err != nil {
		return nil, fmt.Errorf("invalid API URL: %w", err)
	}
	target := *base
	target.Path = strings.TrimRight(base.Path, "/") + task.Attach.Path
	switch target.Scheme {
	case "https":
		target.Scheme = "wss"
	case "http":
		target.Scheme = "ws"
	}
	if tty {
		if cols, rows, sizeErr := term.GetSize(int(stdin.Fd())); sizeErr == nil {
			query := url.Values{}
			query.Set("rows", strconv.Itoa(rows))
			query.Set("cols", strconv.Itoa(cols))
			target.RawQuery = query.Encode()
		}
	}
	headers := http.Header{api.AppTaskAttachTokenHeader: []string{task.Attach.Token}}
	if token := client.Token(); token != "" {
		headers.Set("Authorization", "Bearer "+token)
	}
	dialer := websocket.Dialer{
		Proxy: http.ProxyFromEnvironment, HandshakeTimeout: appExecAttachHandshake,
		Subprotocols: []string{api.AppTaskAttachSubprotocol},
	}
	for {
		conn, resp, dialErr := dialer.DialContext(ctx, target.String(), headers)
		if dialErr == nil {
			return conn, nil
		}
		if resp == nil || resp.StatusCode != http.StatusConflict {
			return nil, attachDialError(dialErr, resp)
		}
		_ = resp.Body.Close()
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("the session did not start in time: %w", ctx.Err())
		case <-time.After(appExecAttachPollInterval):
		}
	}
}

func attachDialError(err error, resp *http.Response) error {
	if resp == nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	var problem api.Problem
	if decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&problem); decodeErr == nil && problem.Detail != "" {
		return fmt.Errorf("%s (HTTP %d)", problem.Detail, resp.StatusCode)
	}
	return fmt.Errorf("%w (HTTP %d)", err, resp.StatusCode)
}

// pumpAppTaskAttach relays stdin, window size, and output until the session
// sends its terminal message.
func pumpAppTaskAttach(ctx context.Context, conn *websocket.Conn, tty bool, stdin *os.File) (api.AppTaskAttachTerminalMessage, error) {
	var writeMu sync.Mutex
	send := func(msg []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteMessage(websocket.BinaryMessage, msg)
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Closing the socket is the only way to interrupt a blocked read.
	go func() {
		<-sessionCtx.Done()
		_ = conn.Close()
	}()

	if tty {
		stopResize := watchTerminalResize(sessionCtx, func() {
			if cols, rows, err := term.GetSize(int(stdin.Fd())); err == nil && rows > 0 && cols > 0 {
				_ = send(api.EncodeAppTaskAttachResize(uint16(min(rows, 65535)), uint16(min(cols, 65535))))
			}
		})
		defer stopResize()
	}
	go func() {
		buf := make([]byte, appExecStdinChunkBytes)
		for {
			n, err := osStdin.Read(buf)
			if n > 0 {
				if sendErr := send(append([]byte{api.AppTaskAttachStdin}, buf[:n]...)); sendErr != nil {
					return
				}
			}
			if err != nil {
				if sessionCtx.Err() == nil {
					_ = send([]byte{api.AppTaskAttachStdinClose})
				}
				return
			}
		}
	}()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return api.AppTaskAttachTerminalMessage{}, ctxErr
			}
			return api.AppTaskAttachTerminalMessage{}, err
		}
		if len(msg) == 0 {
			continue
		}
		switch msg[0] {
		case api.AppTaskAttachStdout:
			_, _ = osStdout.Write(msg[1:])
		case api.AppTaskAttachStderr:
			_, _ = osStderr.Write(msg[1:])
		case api.AppTaskAttachTerminal:
			return api.DecodeAppTaskAttachTerminal(msg[1:])
		}
	}
}

func cancelInteractiveTask(client *api.Client, slug, taskID string) {
	cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := client.CancelAppTask(cancelCtx, slug, taskID); err != nil {
		PrintWarn(osStderr, "could not request cancellation for task %s: %v", taskID, err)
	}
}
