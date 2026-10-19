package cirunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
)

var errTransfer = errors.New("CI runtime transfer unavailable")

type Transport struct {
	origin string
	proof  ci.RuntimeProof
	client *http.Client
}

// NewTransport accepts one HTTPS origin, never a token-bearing URL. Redirects,
// cookie jars, inherited proxies and account Authorization headers are disabled.
func NewTransport(origin string, proof ci.RuntimeProof, provided *http.Client) (*Transport, error) {
	if !ci.ValidRuntimeOrigin(origin) || !ci.ValidRuntimeProof(proof) {
		return nil, errTransfer
	}
	client := http.Client{Timeout: time.Duration(api.CIJobsTransferSeconds) * time.Second}
	if provided != nil {
		client = *provided
	}
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	if httpTransport, ok := base.(*http.Transport); ok {
		copyTransport := httpTransport.Clone()
		copyTransport.Proxy = nil
		client.Transport = copyTransport
	} else {
		client.Transport = base
	}
	if client.Timeout <= 0 || client.Timeout > time.Duration(api.CIJobsTransferSeconds)*time.Second {
		client.Timeout = time.Duration(api.CIJobsTransferSeconds) * time.Second
	}
	return &Transport{origin: strings.TrimSuffix(origin, "/"), proof: proof, client: &client}, nil
}

func (t *Transport) request(ctx context.Context, method, suffix string, body io.Reader) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, t.origin+"/v1/internal/ci/runtime/"+t.proof.RunID+"/"+suffix, body)
	if err != nil {
		return nil, errTransfer
	}
	request.Header.Set(ci.RuntimeInstanceHeader, t.proof.InstanceID)
	request.Header.Set(ci.RuntimeCapabilityHeader, t.proof.Capability)
	return request, nil
}

func (t *Transport) Control(ctx context.Context) (ci.RuntimeView, error) {
	request, err := t.request(ctx, http.MethodGet, "control", nil)
	if err != nil {
		return ci.RuntimeView{}, err
	}
	response, err := t.client.Do(request)
	if err != nil {
		return ci.RuntimeView{}, errTransfer
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return ci.RuntimeView{}, errTransfer
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, api.CIRequestMaxBytes+1))
	if err != nil || len(raw) > api.CIRequestMaxBytes {
		return ci.RuntimeView{}, errTransfer
	}
	var view ci.RuntimeView
	if json.Unmarshal(raw, &view) != nil || ci.ValidateJobsManifest(view.Manifest) != nil || view.Manifest.AttemptID != t.proof.RunID {
		return view, errTransfer
	}
	return view, nil
}

func (t *Transport) source(ctx context.Context) (io.ReadCloser, error) {
	request, err := t.request(ctx, http.MethodGet, "source", nil)
	if err != nil {
		return nil, err
	}
	response, err := t.client.Do(request)
	if err != nil {
		return nil, errTransfer
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return nil, errTransfer
	}
	return response.Body, nil
}

func (t *Transport) upload(ctx context.Context, object ci.RuntimeObject, file *os.File) error {
	for range api.CIJobsTransferMaxAttempts {
		request, err := t.request(ctx, http.MethodPut, "objects", io.NewSectionReader(file, 0, object.Bytes))
		if err != nil {
			return err
		}
		request.ContentLength = object.Bytes
		if object.Bytes == 0 {
			request.Body = http.NoBody
		}
		request.Header.Set("Content-Type", "application/octet-stream")
		request.Header.Set(ci.RuntimeObjectHeader, object.Name)
		request.Header.Set(ci.RuntimeDigestHeader, object.Digest)
		response, err := t.client.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return errTransfer
			}
			continue
		}
		_ = response.Body.Close()
		if response.StatusCode == http.StatusNoContent {
			return nil
		}
		if response.StatusCode < 500 && response.StatusCode != http.StatusTooManyRequests {
			return errTransfer
		}
	}
	return errTransfer
}

func (t *Transport) seal(ctx context.Context, result ci.JobsResultManifest) error {
	raw, err := json.Marshal(result)
	if err != nil || len(raw) > api.CIJobsResultManifestMaxBytes {
		return errTransfer
	}
	for range api.CIJobsTransferMaxAttempts {
		request, err := t.request(ctx, http.MethodPut, "result", bytes.NewReader(raw))
		if err != nil {
			return err
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := t.client.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return errTransfer
			}
			continue
		}
		_ = response.Body.Close()
		if response.StatusCode == http.StatusNoContent {
			return nil
		}
		if response.StatusCode < 500 && response.StatusCode != http.StatusTooManyRequests {
			return errTransfer
		}
	}
	return errTransfer
}
