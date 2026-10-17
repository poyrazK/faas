package objectqualification

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

type inspectionRequest struct {
	Directory string `json:"directory"`
	Daemon    string `json:"daemon"`
	Control   string `json:"control"`
	Role      string `json:"role"`
}

type HostInspection struct {
	Version          int                `json:"version"`
	Role             string             `json:"role"`
	Directory        string             `json:"directory"`
	Platform         string             `json:"platform"`
	KernelBootSHA256 string             `json:"kernel_boot_sha256"`
	DaemonSHA256     string             `json:"daemon_sha256"`
	ControlSHA256    string             `json:"control_sha256"`
	AgentSHA256      string             `json:"agent_sha256"`
	Storage          StorageObservation `json:"storage"`
	Error            string             `json:"error,omitempty"`
}

func (r inspectionRequest) validate() error {
	if r.Role != "primary" && r.Role != "follower" {
		return errors.New("invalid qualification inspection role")
	}
	for _, path := range []string{r.Directory, r.Daemon, r.Control} {
		if !utf8.ValidString(path) || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsAny(path, "\r\n\x00") {
			return errors.New("qualification inspection requires canonical absolute paths")
		}
	}
	for _, path := range []string{r.Daemon, r.Control} {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			return errors.New("qualification inspection requires regular executable node binaries")
		}
	}
	return nil
}

func inspectHost(ctx context.Context, r inspectionRequest) (host HostInspection, err error) {
	host = HostInspection{Version: 1, Role: r.Role, Directory: r.Directory, Platform: runtime.GOOS + "/" + runtime.GOARCH}
	defer func() {
		if err != nil {
			host.Error = err.Error()
		}
	}()
	if err = r.validate(); err != nil {
		return host, err
	}
	if err = ctx.Err(); err != nil {
		return host, err
	}
	if host.KernelBootSHA256, err = kernelBootDigest(); err != nil {
		return host, err
	}
	executable, err := os.Executable()
	if err != nil {
		return host, err
	}
	for _, target := range []struct {
		path   string
		digest *string
	}{{r.Daemon, &host.DaemonSHA256}, {r.Control, &host.ControlSHA256}, {executable, &host.AgentSHA256}} {
		if err = ctx.Err(); err != nil {
			return host, err
		}
		if *target.digest, err = binaryDigest(target.path); err != nil {
			return host, err
		}
	}
	host.Storage, err = inspectStorage(r.Directory, r.Role)
	return host, err
}

// ServeInspection is a single read-only request, independent of agent protocol
// initialization. It never creates work directories, credentials or listeners.
func ServeInspection(ctx context.Context, input io.Reader, output io.Writer) error {
	if closer, ok := input.(io.Closer); ok {
		stop := context.AfterFunc(ctx, func() { _ = closer.Close() })
		defer stop()
	}
	var request inspectionRequest
	if err := decodeInspection(input, &request); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	host, _ := inspectHost(ctx, request)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return json.NewEncoder(output).Encode(host)
}

func decodeInspection(reader io.Reader, value any) error {
	data, err := io.ReadAll(io.LimitReader(reader, int64(api.ObjectQualificationAgentLineBytes)+1))
	if err != nil {
		return err
	}
	if len(data) > api.ObjectQualificationAgentLineBytes {
		return errors.New("qualification inspection exceeds its message budget")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(value); err != nil {
		return errors.New("invalid qualification inspection document")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("qualification inspection accepts one document")
	}
	return nil
}

func validateHostInspection(h HostInspection, request inspectionRequest) error {
	if h.Error != "" {
		return fmt.Errorf("qualification inspection: %s", h.Error)
	}
	if h.Version != 1 || h.Role != request.Role || h.Directory != request.Directory || !strings.HasPrefix(h.Platform, "linux/") {
		return errors.New("qualification inspection identity or target differs")
	}
	if h.Storage.Parent != filepath.Dir(request.Directory) {
		return errors.New("qualification inspection observed another parent directory")
	}
	for _, digest := range []string{h.KernelBootSHA256, h.DaemonSHA256, h.ControlSHA256, h.AgentSHA256} {
		data, err := hex.DecodeString(digest)
		if err != nil || len(data) != 32 || hex.EncodeToString(data) != digest {
			return errors.New("qualification inspection digest is missing or invalid")
		}
	}
	return nil
}
