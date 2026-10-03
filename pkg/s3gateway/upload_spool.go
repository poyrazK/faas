package s3gateway

import (
	"crypto/md5" // #nosec G501 -- S3 Content-MD5 compatibility.
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"net/http"
	"os"
)

func (h *Handler) reserveUploadStage(w http.ResponseWriter, r *http.Request, req requestContext) bool {
	select {
	case h.putSlots <- struct{}{}:
	default:
		closeUnreadUploadConnection(w, r)
		writeS3Error(w, http.StatusServiceUnavailable, "SlowDown", "Please reduce your request rate.", r.URL.Path, req.requestID)
		return false
	}
	if !h.reserveSpool(r.ContentLength) {
		<-h.putSlots
		closeUnreadUploadConnection(w, r)
		writeS3Error(w, http.StatusServiceUnavailable, "SlowDown", "The upload spool does not have enough reserved capacity.", r.URL.Path, req.requestID)
		return false
	}
	return true
}

// A final response to Expect: 100-continue otherwise allows Go's HTTP/1
// client to send the body for connection reuse. This request has no reserved
// staging space, so the connection must terminate after the error response.
func closeUnreadUploadConnection(w http.ResponseWriter, r *http.Request) {
	if r.ProtoMajor == 1 && r.ContentLength > 0 {
		w.Header().Set("Connection", "close")
	}
}
func (h *Handler) stageUpload(w http.ResponseWriter, r *http.Request, req requestContext) (*os.File, func(), bool) {
	if !h.reserveUploadStage(w, r, req) {
		return nil, func() {}, false
	}
	release := func() { h.releaseSpool(r.ContentLength); <-h.putSlots }
	file, err := os.CreateTemp(h.spoolDir, "gregale-s3-put-*")
	if err != nil {
		release()
		writeS3Error(w, http.StatusServiceUnavailable, "ServiceUnavailable", "Gregale could not stage this upload.", r.URL.Path, req.requestID)
		return nil, func() {}, false
	}
	name := file.Name()
	cleanup := func() {
		_ = file.Close()
		if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			h.log.Warn("S3 upload spool cleanup failed", "request_id", req.requestID)
		}
		release()
	}
	if !h.spoolUploadBody(w, r, req, file) {
		cleanup()
		return nil, func() {}, false
	}
	return file, cleanup, true
}
func (h *Handler) spoolUploadBody(w http.ResponseWriter, r *http.Request, req requestContext, file *os.File) bool {
	checksum, expectedChecksum, checksumErr := requestChecksum(r.Header)
	if checksumErr != nil {
		h.writeAWSChunkedError(w, r, req.requestID, checksumErr)
		return false
	}
	sha := sha256.New()
	md5sum := md5.New() // #nosec G401 -- S3 Content-MD5 compatibility.
	writers := []io.Writer{file, sha, md5sum}
	if checksum != nil {
		writers = append(writers, checksum)
	}
	written, err := io.Copy(io.MultiWriter(writers...), io.LimitReader(r.Body, r.ContentLength+1))
	if err != nil || written != r.ContentLength {
		if err != nil && h.writeAWSChunkedError(w, r, req.requestID, err) {
			return false
		}
		writeS3Error(w, http.StatusBadRequest, "IncompleteBody", "You did not provide the number of bytes specified by Content-Length.", r.URL.Path, req.requestID)
		return false
	}
	if !verifyUploadDigests(w, r, req, sha, md5sum, checksum, expectedChecksum) {
		return false
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		writeS3Error(w, http.StatusServiceUnavailable, "ServiceUnavailable", "Gregale could not stage this upload.", r.URL.Path, req.requestID)
		return false
	}
	return true
}

func verifyUploadDigests(w http.ResponseWriter, r *http.Request, req requestContext, sha, md5sum, checksum hash.Hash, expectedChecksum []byte) bool {
	payloadHash := req.signature.PayloadHash
	if payloadHash != "UNSIGNED-PAYLOAD" && !isStreamingPayloadHash(payloadHash) {
		actual := hex.EncodeToString(sha.Sum(nil))
		if subtle.ConstantTimeCompare([]byte(actual), []byte(payloadHash)) != 1 {
			writeS3Error(w, http.StatusBadRequest, "XAmzContentSHA256Mismatch", "The provided x-amz-content-sha256 does not match the request body.", r.URL.Path, req.requestID)
			return false
		}
	}
	if expected := r.Header.Get("Content-MD5"); expected != "" {
		decoded, decodeErr := base64.StdEncoding.DecodeString(expected)
		if decodeErr != nil || len(decoded) != md5.Size || subtle.ConstantTimeCompare(decoded, md5sum.Sum(nil)) != 1 {
			writeS3Error(w, http.StatusBadRequest, "BadDigest", "The Content-MD5 you specified did not match what Gregale received.", r.URL.Path, req.requestID)
			return false
		}
	}
	if checksum != nil && subtle.ConstantTimeCompare(expectedChecksum, checksum.Sum(nil)) != 1 {
		writeS3Error(w, http.StatusBadRequest, "BadDigest", "The checksum you specified did not match what Gregale received.", r.URL.Path, req.requestID)
		return false
	}
	return true
}
