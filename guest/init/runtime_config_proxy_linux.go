//go:build linux

package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

const (
	metadataEnvPath     = "/v1/metadata/env"
	metadataEnvEndpoint = "http://169.254.169.254" + metadataEnvPath
	// Must mirror pkg/fcvm.VsockRuntimeConfigHostPort. guest-init keeps this
	// literal local to preserve the one-way package dependency.
	metadataEnvHostPort   = 1031
	runtimeConfigMaxFrame = 32 << 10
)

type runtimeConfigRequest struct {
	Scope string `json:"scope"`
}

type runtimeConfigResponse struct {
	Env      map[string]string `json:"env,omitempty"`
	Revision string            `json:"revision,omitempty"`
	Error    string            `json:"error,omitempty"`
}

// dialRuntimeConfigHost is a variable so the HTTP seam can be exercised with
// net.Pipe without requiring an AF_VSOCK device in unit tests.
var dialRuntimeConfigHost = dialRuntimeConfigHostVsock

func metadataEnvHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeRuntimeConfigError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if r.URL.Query().Get("scope") != "" && r.URL.Query().Get("scope") != "default" {
		writeRuntimeConfigError(w, http.StatusBadRequest, "unsupported_scope")
		return
	}
	conn, err := dialRuntimeConfigHost()
	if err != nil {
		writeRuntimeConfigError(w, http.StatusServiceUnavailable, "config_unavailable")
		return
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
	reqBody, _ := json.Marshal(runtimeConfigRequest{Scope: "default"})
	if err := writeRuntimeConfigFrame(conn, reqBody); err != nil {
		writeRuntimeConfigError(w, http.StatusServiceUnavailable, "config_unavailable")
		return
	}
	body, err := readRuntimeConfigFrame(conn)
	if err != nil {
		writeRuntimeConfigError(w, http.StatusServiceUnavailable, "config_unavailable")
		return
	}
	var response runtimeConfigResponse
	if err := json.Unmarshal(body, &response); err != nil {
		writeRuntimeConfigError(w, http.StatusServiceUnavailable, "config_unavailable")
		return
	}
	if response.Error != "" {
		status := http.StatusServiceUnavailable
		if response.Error == "invalid_request" || response.Error == "unsupported_scope" {
			status = http.StatusBadRequest
		}
		writeRuntimeConfigError(w, status, response.Error)
		return
	}
	if response.Env == nil {
		response.Env = map[string]string{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func writeRuntimeConfigError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(runtimeConfigResponse{Error: code})
}

func dialRuntimeConfigHostVsock() (net.Conn, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	if err := unix.Connect(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_HOST, Port: metadataEnvHostPort}); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "runtime-config-vsock")
	conn, err := net.FileConn(file)
	_ = file.Close()
	if err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	return conn, nil
}

func writeRuntimeConfigFrame(w io.Writer, body []byte) error {
	if len(body) == 0 || len(body) > runtimeConfigMaxFrame {
		return fmt.Errorf("invalid frame length %d", len(body))
	}
	frame := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(body)))
	copy(frame[4:], body)
	for len(frame) > 0 {
		n, err := w.Write(frame)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}

func readRuntimeConfigFrame(r io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > runtimeConfigMaxFrame {
		return nil, fmt.Errorf("invalid frame length %d", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}

// StampRuntimeConfigEnv advertises the opt-in live configuration endpoint.
// The endpoint itself is platform-owned; customer env cannot redirect it.
func StampRuntimeConfigEnv(env []string) []string {
	return append(env, "FAAS_METADATA_ENV_ENDPOINT="+metadataEnvEndpoint)
}
