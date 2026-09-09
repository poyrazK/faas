//go:build linux

package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

const (
	workloadIdentityListenAddr = "127.0.0.1:2773"
	workloadIdentityTokenPath  = "/oidc/token"
	workloadIdentityHostPort   = 1030
	workloadIdentityMaxFrame   = 4096
)

// workloadIdentityEndpoint is intentionally loopback-only. The app process
// can reach it without a secret, while the guest network policy keeps the
// host-vsock credential exchange inaccessible to neighboring VMs.
const workloadIdentityEndpoint = "http://127.0.0.1:2773" + workloadIdentityTokenPath

type workloadIdentityGuestRequest struct {
	Audience string `json:"audience"`
}

type workloadIdentityGuestResponse struct {
	AccessToken string `json:"access_token,omitempty"`
	TokenType   string `json:"token_type,omitempty"`
	ExpiresIn   int64  `json:"expires_in,omitempty"`
	Error       string `json:"error,omitempty"`
}

// startWorkloadIdentityProxy exposes a tiny AWS/GCP-friendly token endpoint
// inside the guest. It forwards each request over the guest's AF_VSOCK link to
// the host; no token is staged in the image or environment.
func startWorkloadIdentityProxy(log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	ln, err := net.Listen("tcp4", workloadIdentityListenAddr)
	if err != nil {
		return fmt.Errorf("workload identity proxy listen: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc(workloadIdentityTokenPath, workloadIdentityTokenHandler)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Warn("workload identity proxy stopped", "err", err)
		}
	}()
	log.Info("workload identity proxy started", "endpoint", workloadIdentityEndpoint)
	return nil
}

func workloadIdentityTokenHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeIdentityGuestError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	audience := r.URL.Query().Get("audience")
	if audience == "" {
		audience = r.Header.Get("X-Faas-Identity-Audience")
	}
	if audience == "" {
		writeIdentityGuestError(w, http.StatusBadRequest, "audience_required")
		return
	}
	conn, err := dialWorkloadIdentityHost()
	if err != nil {
		writeIdentityGuestError(w, http.StatusServiceUnavailable, "identity_unavailable")
		return
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
	reqBody, _ := json.Marshal(workloadIdentityGuestRequest{Audience: audience})
	if err := writeIdentityFrame(conn, reqBody); err != nil {
		writeIdentityGuestError(w, http.StatusServiceUnavailable, "identity_unavailable")
		return
	}
	body, err := readIdentityFrameConn(conn)
	if err != nil {
		writeIdentityGuestError(w, http.StatusServiceUnavailable, "identity_unavailable")
		return
	}
	var response workloadIdentityGuestResponse
	if err := json.Unmarshal(body, &response); err != nil {
		writeIdentityGuestError(w, http.StatusServiceUnavailable, "identity_unavailable")
		return
	}
	if response.Error != "" {
		status := http.StatusServiceUnavailable
		if response.Error == "invalid_audience" || response.Error == "invalid_request" {
			status = http.StatusBadRequest
		}
		writeIdentityGuestError(w, status, response.Error)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func writeIdentityGuestError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(workloadIdentityGuestResponse{Error: code})
}

func dialWorkloadIdentityHost() (net.Conn, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	if err := unix.Connect(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_HOST, Port: workloadIdentityHostPort}); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "workload-identity-vsock")
	conn, err := net.FileConn(file)
	_ = file.Close()
	if err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	return conn, nil
}

func writeIdentityFrame(w io.Writer, body []byte) error {
	if len(body) == 0 || len(body) > workloadIdentityMaxFrame {
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

func readIdentityFrameConn(r io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > workloadIdentityMaxFrame {
		return nil, fmt.Errorf("invalid frame length %d", n)
	}
	body := make([]byte, n)
	_, err := io.ReadFull(r, body)
	return body, err
}

// StampWorkloadIdentityEnv adds the endpoint to a workload's environment.
// The variable is platform-owned and is appended after customer values by the
// caller, so an image cannot redirect identity requests to an external host.
func StampWorkloadIdentityEnv(env []string) []string {
	return append(env, "FAAS_WORKLOAD_IDENTITY_ENDPOINT="+workloadIdentityEndpoint)
}
