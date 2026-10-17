package cirunner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
)

func (r Runner) prepareInputs(ctx context.Context, root *os.Root) error {
	if len(r.Manifest.Inputs) == 0 {
		return nil
	}
	// Refuse source collisions. No archive is extracted and no repository path
	// or earlier input can be overwritten by a download.
	if err := root.Mkdir(ci.ArtifactInputsDirectory, 0o700); err != nil {
		return errTransfer
	}
	for _, input := range r.Manifest.Inputs {
		if err := r.downloadInput(ctx, root, input); err != nil {
			return err
		}
	}
	return nil
}

func (r Runner) downloadInput(ctx context.Context, root *os.Root, input ci.JobsInput) error {
	for range api.CIJobsTransferMaxAttempts {
		if ctx.Err() != nil {
			return errTransfer
		}
		file, err := root.OpenFile(ci.ArtifactInputsDirectory+"/"+input.Name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return errTransfer
		}
		request, err := r.Transport.request(ctx, http.MethodGet, "inputs/"+input.Name, nil)
		if err != nil {
			_ = file.Close()
			return err
		}
		response, requestErr := r.Transport.client.Do(request)
		if requestErr == nil {
			hash := sha256.New()
			count, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, input.Bytes+1))
			_ = response.Body.Close()
			closeErr := file.Close()
			if response.StatusCode == http.StatusOK && response.Header.Get(ci.RuntimeDigestHeader) == input.Digest && response.ContentLength == input.Bytes && count == input.Bytes && copyErr == nil && closeErr == nil && "sha256:"+hex.EncodeToString(hash.Sum(nil)) == input.Digest {
				return nil
			}
			_ = root.Remove(ci.ArtifactInputsDirectory + "/" + input.Name)
			if response.StatusCode < 500 && response.StatusCode != http.StatusTooManyRequests && response.StatusCode != http.StatusOK {
				return errTransfer
			}
		} else {
			_ = file.Close()
			_ = root.Remove(ci.ArtifactInputsDirectory + "/" + input.Name)
		}
	}
	return errTransfer
}
