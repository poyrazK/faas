package bindingprobe

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/onebox-faas/faas/pkg/api"
)

// The name cannot be supplied by an application or CLI caller. Never overwrite
// a customer object, and delete the exact returned version in versioned buckets.
func objectStorageWrite(ctx context.Context, client *s3.Client, observation *objectStorageTransport, bucket string, report api.ObjectStorageBindingProbeReport) (result api.ObjectStorageBindingProbeReport) {
	result = report
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		result.Error = "object-storage probe identity unavailable"
		return
	}
	key := ".gregale/binding-probes/" + hex.EncodeToString(nonce)
	payload := []byte("gregale object-storage binding probe\n")
	output, err := client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key),
		Body: bytes.NewReader(payload), ContentLength: aws.Int64(int64(len(payload))), IfNoneMatch: aws.String("*")})
	if observation.status != 0 {
		result.Connection = api.ObjectStorageBindingProbeCheck{Status: "passed", Detail: "S3 endpoint returned a response"}
	} else {
		result.Connection = api.ObjectStorageBindingProbeCheck{Status: "failed"}
	}
	if err != nil || output == nil {
		result.Upload.Status = "failed"
		result.Error = "object-storage probe upload failed"
		if observation.status == http.StatusForbidden || observation.status == http.StatusUnauthorized {
			result.Authorization.Status = "failed"
		}
		// An ambiguous PUT may have committed. Attempt bounded cleanup but never
		// attest cleanup: a lost response may also have lost its version identity.
		if observation.status == 0 || observation.status >= 500 || observation.status >= 200 && observation.status < 300 {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), objectStorageTimeout)
			defer cancel()
			_, _ = client.DeleteObject(cleanupCtx, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
			result.Cleanup.Status = "failed"
			result.Cleanup.Detail = "upload outcome is ambiguous; cleanup could not be verified"
		}
		return
	}
	result.Upload = &api.ObjectStorageBindingProbeCheck{Status: "passed", Detail: "tiny conditional upload acknowledged"}
	result.Authorization = api.ObjectStorageBindingProbeCheck{Status: "passed", Detail: "injected credentials accepted for upload"}
	version := output.VersionId
	defer func() {
		// Cancellation or a failed GET must not suppress cleanup. The total
		// operation and cleanup budgets remain below the platform task deadline.
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), objectStorageTimeout)
		defer cancel()
		deleted, deleteErr := client.DeleteObject(cleanupCtx, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), VersionId: version})
		if deleteErr != nil || deleted == nil {
			result.Cleanup.Status = "failed"
			result.Error = "object-storage probe cleanup failed"
			return
		}
		result.Cleanup = &api.ObjectStorageBindingProbeCheck{Status: "passed", Detail: "probe deletion acknowledged; write-only permission cannot verify absence"}
		if result.Mode == "read_write" {
			remaining, readErr := client.GetObject(cleanupCtx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), VersionId: version})
			if remaining != nil && remaining.Body != nil {
				_ = remaining.Body.Close()
			}
			var providerError smithy.APIError
			if !errors.As(readErr, &providerError) || (providerError.ErrorCode() != "NoSuchKey" && providerError.ErrorCode() != "NoSuchVersion") {
				result.Cleanup.Status = "failed"
				result.Error = "object-storage probe deletion could not be verified"
				return
			}
			result.Cleanup.Detail = "exact probe version is absent after deletion"
		}
		if result.Error == "" {
			result.BucketAccess = api.ObjectStorageBindingProbeCheck{Status: "passed", Detail: "requested object operations and cleanup succeeded"}
		}
	}()
	if result.Mode == "read_write" {
		result.Download.Status = "failed"
		download, readErr := client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), VersionId: version})
		if readErr != nil || download == nil || download.Body == nil {
			result.Error = "object-storage probe download failed"
			return
		}
		data, bodyErr := io.ReadAll(io.LimitReader(download.Body, int64(len(payload)+1)))
		closeErr := download.Body.Close()
		if bodyErr != nil || closeErr != nil || !bytes.Equal(data, payload) {
			result.Error = "object-storage probe contents did not match"
			return
		}
		result.Download = &api.ObjectStorageBindingProbeCheck{Status: "passed", Detail: "exact uploaded bytes verified"}
	}
	return
}
