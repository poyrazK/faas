package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

func uploadBucketMultipart(ctx context.Context, c bucketTransferClient, o bucketTransferOptions, file *os.File, size int64) (bucketTransferResult, error) {
	hash, _, err := bucketFileFingerprint(ctx, file, size, 0)
	if err != nil {
		return bucketTransferResult{}, err
	}
	u, err := c.CreateObjectMultipartUpload(ctx, o.app, o.bucket, api.CreateObjectMultipartUploadRequest{Key: o.key, SizeBytes: size, ContentType: o.contentType})
	if err != nil {
		return bucketTransferResult{}, err
	}
	result := bucketTransferResult{Key: o.key, Path: o.path, Bytes: size, UploadID: u.ID, Status: "pending"}
	if err = validateBucketMultipartSession(u, o, size); err != nil {
		return result, err
	}
	if u.ContentType != o.contentType {
		return result, errors.New("multipart content type differs from the requested value")
	}
	path, err := bucketUploadCheckpointPath(c.BaseURL(), u.ID)
	if err != nil {
		return result, err
	}
	lock, err := lockBucketUploadCheckpoint(path)
	if err != nil {
		return result, err
	}
	defer func() { _ = lock.Close() }()
	if _, err = os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return result, errors.New("upload checkpoint already exists or cannot be inspected; use --resume")
	}
	currentHash, parts, err := bucketFileFingerprint(ctx, file, size, u.PartSizeBytes)
	if err != nil || currentHash != hash {
		return result, errors.New("upload source changed before multipart transfer")
	}
	cp := bucketUploadCheckpoint{Version: bucketUploadCheckpointVersion, APIBase: c.BaseURL(), App: o.app, Bucket: o.bucket, UploadID: u.ID, Key: o.key, Size: size, ContentType: u.ContentType, SHA256: hash, PartSize: u.PartSizeBytes, Phase: "uploading", Parts: parts}
	if err = saveBucketUploadCheckpoint(path, cp); err != nil {
		return result, err
	}
	if u.State != "active" || !u.ExpiresAt.After(time.Now()) {
		return result, errors.New("multipart session is not active; inspect its status")
	}
	return continueBucketMultipart(ctx, c, o, file, u, path, cp, nil)
}

func resumeBucketMultipart(ctx context.Context, c bucketTransferClient, o bucketTransferOptions, file *os.File, size int64) (bucketTransferResult, error) {
	result := bucketTransferResult{Key: o.key, Path: o.path, Bytes: size, UploadID: o.resumeID, Status: "pending"}
	path, err := bucketUploadCheckpointPath(c.BaseURL(), o.resumeID)
	if err != nil {
		return result, err
	}
	lock, err := lockBucketUploadCheckpoint(path)
	if err != nil {
		return result, err
	}
	defer func() { _ = lock.Close() }()
	cp, err := loadBucketUploadCheckpoint(path)
	if err != nil {
		return result, err
	}
	if cp.APIBase != c.BaseURL() || cp.App != o.app || cp.Bucket != o.bucket || cp.UploadID != o.resumeID || cp.Key != o.key || cp.Size != size || o.contentTypeSet && o.contentType != cp.ContentType {
		return result, errors.New("upload checkpoint does not match this destination, source size or content type")
	}
	if err = discardBucketStagedPart(path); err != nil {
		return result, err
	}
	hash, _, err := bucketFileFingerprint(ctx, file, size, cp.PartSize)
	if err != nil || hash != cp.SHA256 {
		return result, errors.New("upload source differs from its saved fingerprint")
	}
	u, err := c.GetObjectMultipartUpload(ctx, o.app, o.bucket, o.resumeID)
	if err != nil {
		return result, err
	}
	if u.ID != cp.UploadID || u.PartSizeBytes != cp.PartSize || int(u.PartCount) != len(cp.Parts) || u.ContentType != cp.ContentType {
		return result, errors.New("multipart session differs from its saved checkpoint")
	}
	if err = validateBucketMultipartSession(u, o, size); err != nil {
		return result, err
	}
	if u.State == "completed" {
		if cp.Phase == "uploading" {
			return result, errors.New("multipart completion was not submitted by this checkpoint")
		}
		return finishBucketMultipart(o, u, path, cp)
	}
	if cp.Phase == "completed" || u.State != "active" && u.State != "completing" || u.State == "active" && !u.ExpiresAt.After(time.Now()) {
		return result, errors.New("multipart session is unavailable for resume; inspect its status")
	}
	if cp.Phase == "completing" {
		return completeBucketMultipart(ctx, c, o, u, path, cp)
	}
	if u.State != "active" {
		return result, errors.New("multipart completion is already pending outside this checkpoint; inspect its status")
	}
	confirmed, err := listBucketResumeParts(ctx, c, o, u, cp)
	if err != nil {
		return result, err
	}
	return continueBucketMultipart(ctx, c, o, file, u, path, cp, confirmed)
}

func validateBucketMultipartSession(u api.ObjectMultipartUpload, o bucketTransferOptions, size int64) error {
	if !validBucketUploadID(u.ID) || u.Key != o.key || u.SizeBytes != size || size <= 0 || size > api.MaxObjectUploadBytes || u.PartSizeBytes <= 0 || u.PartSizeBytes > api.MaxObjectSinglePutBytes || u.PartCount < 1 || u.PartCount > api.MaxMultipartParts || int64(u.PartCount) != (size-1)/u.PartSizeBytes+1 {
		return errors.New("invalid multipart session geometry")
	}
	return nil
}

func validBucketPartETag(etag string) bool {
	return etag != "" && len(etag) <= api.MaxObjectWriteETagBytes && utf8.ValidString(etag) && !strings.ContainsAny(etag, "\x00\r\n")
}

func listBucketResumeParts(ctx context.Context, c bucketTransferClient, o bucketTransferOptions, u api.ObjectMultipartUpload, cp bucketUploadCheckpoint) (map[int32]string, error) {
	confirmed := make(map[int32]string)
	marker := int32(0)
	for pages := 0; pages < api.MaxMultipartParts; pages++ {
		page, err := c.ListObjectMultipartParts(ctx, o.app, o.bucket, u.ID, int(marker), api.MaxObjectS3ListItems)
		if err != nil {
			return nil, err
		}
		if page.Items == nil || len(page.Items) > api.MaxObjectS3ListItems {
			return nil, errors.New("multipart part page is incomplete or oversized")
		}
		last := marker
		for _, part := range page.Items {
			if part.PartNumber <= last || part.PartNumber > u.PartCount || !validBucketPartETag(part.ETag) || part.SizeBytes != min(u.PartSizeBytes, u.SizeBytes-int64(part.PartNumber-1)*u.PartSizeBytes) {
				return nil, errors.New("invalid provider-confirmed multipart parts")
			}
			local := cp.Parts[part.PartNumber-1]
			if !local.Attempted || local.ETag != "" && local.ETag != part.ETag {
				return nil, errors.New("provider part differs from its checkpoint; inspect the upload session")
			}
			// A lost PUT acknowledgment has no locally verified ETag. Explicit
			// resume replaces that part with staged, fingerprint-matching bytes.
			if local.ETag != "" {
				confirmed[part.PartNumber] = part.ETag
			}
			last = part.PartNumber
		}
		if page.NextPartNumberMarker == 0 {
			return confirmed, nil
		}
		if last <= marker || page.NextPartNumberMarker != last || last >= u.PartCount {
			return nil, errors.New("invalid multipart part continuation")
		}
		marker = last
	}
	return nil, errors.New("multipart parts exceeded the bounded scan")
}

func continueBucketMultipart(ctx context.Context, c bucketTransferClient, o bucketTransferOptions, file *os.File, u api.ObjectMultipartUpload, path string, cp bucketUploadCheckpoint, confirmed map[int32]string) (bucketTransferResult, error) {
	result := bucketTransferResult{Key: o.key, Path: o.path, Bytes: u.SizeBytes, UploadID: u.ID, Status: "pending"}
	for part := int32(1); part <= u.PartCount; part++ {
		if confirmed[part] != "" {
			continue
		}
		if err := uploadBucketCheckpointPart(ctx, c, o, file, u, part, path, &cp); err != nil {
			return result, err
		}
	}
	hash, _, err := bucketFileFingerprint(ctx, file, u.SizeBytes, cp.PartSize)
	if err != nil || hash != cp.SHA256 {
		return result, errors.New("upload source changed before completion; the session remains pending")
	}
	cp.Phase = "completing"
	if err = saveBucketUploadCheckpoint(path, cp); err != nil {
		return result, err
	}
	return completeBucketMultipart(ctx, c, o, u, path, cp)
}

func uploadBucketCheckpointPart(ctx context.Context, c bucketTransferClient, o bucketTransferOptions, file *os.File, u api.ObjectMultipartUpload, part int32, path string, cp *bucketUploadCheckpoint) error {
	offset := int64(part-1) * u.PartSizeBytes
	size := min(u.PartSizeBytes, u.SizeBytes-offset)
	staged, err := stageBucketUploadPart(ctx, file, offset, size, cp.Parts[part-1].SHA256, path)
	if err != nil {
		return err
	}
	defer func() { _ = staged.Close(); _ = os.Remove(staged.Name()) }()
	cp.Parts[part-1].Attempted, cp.Parts[part-1].ETag = true, ""
	if err = saveBucketUploadCheckpoint(path, *cp); err != nil {
		return err
	}
	signed, err := c.SignObjectMultipartPart(ctx, o.app, o.bucket, u.ID, int(part), api.ObjectMultipartPartSignRequest{})
	if err != nil {
		return err
	}
	response, err := executeBucketCapability(ctx, signed, http.MethodPut, staged, size)
	if err != nil {
		return err
	}
	etag, err := bucketTransferETag(response)
	_ = response.Body.Close()
	if err != nil {
		return err
	}
	cp.Parts[part-1].ETag = etag
	return saveBucketUploadCheckpoint(path, *cp)
}

func completeBucketMultipart(ctx context.Context, c bucketTransferClient, o bucketTransferOptions, u api.ObjectMultipartUpload, path string, cp bucketUploadCheckpoint) (bucketTransferResult, error) {
	result := bucketTransferResult{Key: o.key, Path: o.path, Bytes: u.SizeBytes, UploadID: u.ID, Status: "pending"}
	parts := make([]api.ObjectMultipartCompletedPart, len(cp.Parts))
	for i, part := range cp.Parts {
		parts[i] = api.ObjectMultipartCompletedPart{PartNumber: int32(i + 1), ETag: part.ETag}
	}
	done, err := c.CompleteObjectMultipartUpload(ctx, o.app, o.bucket, u.ID, api.CompleteObjectMultipartUploadRequest{Parts: parts})
	if err != nil {
		return result, fmt.Errorf("multipart completion outcome is pending; resume the same upload to recover: %w", err)
	}
	if done.ID != u.ID || done.PartSizeBytes != u.PartSizeBytes || done.PartCount != u.PartCount || done.ContentType != u.ContentType {
		return result, errors.New("multipart completion differs from its session; inspect its status")
	}
	return finishBucketMultipart(o, done, path, cp)
}

func finishBucketMultipart(o bucketTransferOptions, done api.ObjectMultipartUpload, path string, cp bucketUploadCheckpoint) (bucketTransferResult, error) {
	result := bucketTransferResult{Key: o.key, Path: o.path, Bytes: cp.Size, UploadID: cp.UploadID, Status: "pending"}
	if done.State != "completed" || done.ID != cp.UploadID || done.Key != o.key || done.SizeBytes != cp.Size || !validBucketPartETag(done.ETag) || done.VersionID != "" && done.VersionID != "null" && !validBucketImmutableVersion(done.VersionID) {
		return result, errors.New("multipart completion acknowledgment is invalid or pending; inspect its status")
	}
	cp.Phase = "completed"
	if err := saveBucketUploadCheckpoint(path, cp); err != nil {
		return result, err
	}
	result.ETag, result.VersionID, result.Status = done.ETag, done.VersionID, "completed"
	return result, nil
}
