//go:build unix

package wire

// Pre-traffic checks for the two-generation hand-off
// (docs/adr/20261020214632-automatic-unit-deploy-safety-rails.md, section 4;
// docs/adr/1736-zero-downtime-daemon-restarts.md).
//
// On the control plane, one host, a deploy's canary is production: once a
// successor reports ready it accepts from the shared sockets at once. With
// checks, the old generation first sends its successor a few requests over
// connected socket pairs, one per check, which the successor's ready gate on
// the matching listener returns from Accept before the successor serves
// anyone else. Each request runs through the same http.Server, middleware
// and handlers that will serve customers. Only when every check answered as
// expected does the old generation let the successor open its listeners;
// otherwise it stops the successor, keeps serving, and reports the
// rejection, which deployctl treats as a failed activation (no restart onto
// the new binary).
//
// The checks are the old generation's: a contract the serving release holds
// the next one to. A successor that predates checks reports ready without
// them and is handed off unchecked, as before.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/daemonunit"
)

// handoffChecks is the old generation's side of a checked hand-off.
type handoffChecks struct {
	checks []HandoffCheck
	local  []*os.File // our ends of the socket pairs, one per check
	remote []*os.File // the successor's ends
	goR    *os.File   // the successor's go pipe
	goW    *os.File
}

// prepareHandoffChecks makes a socket pair per check and the go pipe. It
// refuses a check whose listener this process does not serve: that is a
// configuration error, not the successor's.
func prepareHandoffChecks(checks []HandoffCheck, serves func(network, address string) bool) (*handoffChecks, error) {
	if len(checks) == 0 {
		return nil, nil
	}
	if _, err := os.Stat(handoffChecksSkipFile); err == nil {
		return nil, nil
	}
	hc := &handoffChecks{checks: checks}
	for _, c := range checks {
		if c.Path == "" || !strings.HasPrefix(c.Path, "/") || len(c.Want) == 0 {
			hc.close()
			return nil, fmt.Errorf("wire: pre-traffic check %q needs a path and wanted statuses", c.Name)
		}
		if !serves(c.Network, c.Address) {
			hc.close()
			return nil, fmt.Errorf("wire: pre-traffic check %q: this process serves no %s listener on %s", c.Name, c.Network, c.Address)
		}
		fds, err := socketPair()
		if err != nil {
			hc.close()
			return nil, fmt.Errorf("wire: pre-traffic check socket pair: %w", err)
		}
		hc.local = append(hc.local, os.NewFile(uintptr(fds[0]), "handoff-check"))
		hc.remote = append(hc.remote, os.NewFile(uintptr(fds[1]), "handoff-check"))
	}
	r, w, err := os.Pipe()
	if err != nil {
		hc.close()
		return nil, fmt.Errorf("wire: hand-off go pipe: %w", err)
	}
	hc.goR, hc.goW = r, w
	return hc, nil
}

// socketPair is a connected unix stream pair, close-on-exec (darwin has no
// SOCK_CLOEXEC, so the flag is set under the fork lock, as package net does).
func socketPair() ([2]int, error) {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		return fds, err
	}
	unix.CloseOnExec(fds[0])
	unix.CloseOnExec(fds[1])
	return fds, nil
}

// extraFiles are the descriptors the successor gets after its ready pipe:
// the check connections, then the go pipe.
func (hc *handoffChecks) extraFiles() []*os.File {
	return append(append([]*os.File(nil), hc.remote...), hc.goR)
}

// env describes extraFiles for a successor whose first one is at fd first.
func (hc *handoffChecks) env(first int) []string {
	specs := make([]checkFD, len(hc.checks))
	for i, c := range hc.checks {
		specs[i] = checkFD{FD: first + i, Network: c.Network, Address: c.Address}
	}
	raw, _ := json.Marshal(specs)
	return []string{
		handoffCheckFDsEnv + "=" + string(raw),
		daemonunit.HandoffGoFDEnv + "=" + strconv.Itoa(first+len(hc.checks)),
	}
}

// started drops our copies of the successor's descriptors.
func (hc *handoffChecks) started() {
	closeFiles(hc.remote)
	hc.remote = nil
	if hc.goR != nil {
		_ = hc.goR.Close()
		hc.goR = nil
	}
}

func (hc *handoffChecks) close() {
	if hc == nil {
		return
	}
	hc.started()
	closeFiles(hc.local)
	hc.local = nil
	if hc.goW != nil {
		_ = hc.goW.Close()
		hc.goW = nil
	}
}

// run sends every check and returns the first failure, wrapped in
// ErrHandoffRejected.
func (hc *handoffChecks) run() error {
	for i, c := range hc.checks {
		if err := runHandoffCheck(c, hc.local[i]); err != nil {
			return fmt.Errorf("%w %s: %w", ErrHandoffRejected, c.Name, err)
		}
	}
	return nil
}

// release lets the successor open its listeners.
func (hc *handoffChecks) release() error {
	_, err := hc.goW.WriteString(daemonunit.HandoffGoMessage + "\n")
	_ = hc.goW.Close()
	hc.goW = nil
	return err
}

func runHandoffCheck(c HandoffCheck, f *os.File) error {
	conn, err := net.FileConn(f)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = handoffCheckDefaultTimeout
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	method, host := c.Method, c.Host
	if method == "" {
		method = http.MethodGet
	}
	if host == "" {
		host = "localhost"
	}
	req, err := http.NewRequest(method, "http://"+host+c.Path, nil) //nolint:noctx // bounded by the connection deadline
	if err != nil {
		return err
	}
	req.Close = true
	req.Header.Set("User-Agent", "gregale-handoff-check")
	req.Header.Set(handoffCheckHeader, c.Name)
	if err := req.Write(conn); err != nil {
		return fmt.Errorf("send %s %s: %w", method, c.Path, err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		return fmt.Errorf("%s %s: no answer: %w", method, c.Path, err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	if !slices.Contains(c.Want, resp.StatusCode) {
		return fmt.Errorf("%s %s answered %d, want %v", method, c.Path, resp.StatusCode, c.Want)
	}
	return nil
}
