//go:build linux

package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

const (
	metadataEnvPath                 = "/v1/metadata/env"
	metadataEnvEndpoint             = "http://169.254.169.254" + metadataEnvPath
	metadataSecretReloadAckPath     = "/v1/metadata/secrets/reload-ack"
	metadataSecretReloadAckEndpoint = "http://169.254.169.254" + metadataSecretReloadAckPath
	// Must mirror pkg/fcvm.VsockRuntimeConfigHostPort. guest-init keeps this
	// literal local to preserve the one-way package dependency.
	metadataEnvHostPort   = 1031
	runtimeConfigMaxFrame = 24 << 20
)

type runtimeConfigRequest struct {
	Kind                    string `json:"kind,omitempty"`
	Scope                   string `json:"scope"`
	Revision                string `json:"revision,omitempty"`
	Projection              string `json:"projection,omitempty"`
	Signal                  string `json:"signal,omitempty"`
	ErrorCode               string `json:"error_code,omitempty"`
	ApplicationAck          string `json:"application_ack,omitempty"`
	ApplicationAckErrorCode string `json:"application_ack_error_code,omitempty"`
}

type runtimeConfigResponse struct {
	Env       map[string]string  `json:"env,omitempty"`
	Secrets   *map[string]string `json:"secrets,omitempty"`
	Revision  string             `json:"revision,omitempty"`
	Unchanged bool               `json:"unchanged,omitempty"`
	Accepted  bool               `json:"accepted,omitempty"`
	Error     string             `json:"error,omitempty"`
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
	reqBody, _ := json.Marshal(runtimeConfigRequest{Kind: "env", Scope: "default"})
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

func metadataSecretReloadAckHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeRuntimeConfigError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var request struct {
		Revision string `json:"revision"`
		Status   string `json:"status"`
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4097))
	if err != nil || len(body) == 0 || len(body) > 4096 {
		writeRuntimeConfigError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || !validGuestRuntimeSecretRevision(request.Revision) ||
		(request.Status != "applied" && request.Status != "failed") {
		writeRuntimeConfigError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeRuntimeConfigError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := sendRuntimeSecretApplicationAck(request.Revision, request.Status); err != nil {
		var remoteError *runtimeConfigResponseError
		if errors.As(err, &remoteError) {
			writeRuntimeConfigError(w, runtimeSecretAckHTTPStatus(remoteError.code), remoteError.code)
			return
		}
		writeRuntimeConfigError(w, http.StatusServiceUnavailable, "secrets_unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_, _ = io.WriteString(w, `{"accepted":true}`)
}

type runtimeConfigResponseError struct{ code string }

func (e *runtimeConfigResponseError) Error() string { return e.code }

func runtimeSecretAckHTTPStatus(code string) int {
	switch code {
	case "invalid_request":
		return http.StatusBadRequest
	case "secret_reload_stale":
		return http.StatusConflict
	default:
		return http.StatusServiceUnavailable
	}
}

func sendRuntimeSecretApplicationAck(revision, status string) error {
	conn, err := dialRuntimeConfigHost()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
	errorCode := ""
	if status == "failed" {
		errorCode = "application_reload_failed"
	}
	body, err := json.Marshal(runtimeConfigRequest{
		Kind: "secret_reload_ack", Revision: revision, ApplicationAck: status,
		ApplicationAckErrorCode: errorCode,
	})
	if err != nil {
		return err
	}
	if err := writeRuntimeConfigFrame(conn, body); err != nil {
		return err
	}
	frame, err := readRuntimeConfigFrame(conn)
	if err != nil {
		return err
	}
	var response runtimeConfigResponse
	if err := json.Unmarshal(frame, &response); err != nil {
		return err
	}
	if response.Error != "" {
		return &runtimeConfigResponseError{code: response.Error}
	}
	if !response.Accepted || response.Revision != revision {
		return errors.New("runtime secret application acknowledgement rejected")
	}
	return nil
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
