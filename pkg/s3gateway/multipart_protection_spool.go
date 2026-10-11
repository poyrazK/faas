package s3gateway

import (
	"crypto/md5" // #nosec G501 -- S3 wire integrity, not authentication.
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"os"
)

// The caller already owns a transfer slot and admission. Stage only protected
// parts, sharing PUT's aggregate disk reservation and free-space floor.
func (h *Handler) stageProtectedMultipartPart(w http.ResponseWriter, r *http.Request, req requestContext, body *requestIntegrityReader) (*os.File, string, func(), bool) {
	if !h.reserveSpool(r.ContentLength) {
		closeUnreadUploadConnection(w, r)
		writeS3Error(w, http.StatusServiceUnavailable, "SlowDown", "The upload spool does not have enough reserved capacity.", r.URL.Path, req.requestID)
		return nil, "", func() {}, false
	}
	file, err := os.CreateTemp(h.spoolDir, "gregale-s3-part-*")
	if err != nil {
		h.releaseSpool(r.ContentLength)
		writeS3Error(w, http.StatusServiceUnavailable, "ServiceUnavailable", "Gregale could not stage this upload.", r.URL.Path, req.requestID)
		return nil, "", func() {}, false
	}
	cleanup := func() {
		_ = file.Close()
		if err := os.Remove(file.Name()); err != nil && !errors.Is(err, os.ErrNotExist) {
			h.log.Warn("S3 part spool cleanup failed", "request_id", req.requestID)
		}
		h.releaseSpool(r.ContentLength)
	}
	digest := md5.New() // #nosec G401 -- Required native S3 Content-MD5 checksum.
	written, err := io.Copy(io.MultiWriter(file, digest), io.LimitReader(body, r.ContentLength+1))
	if err != nil || written != r.ContentLength || body.err != nil {
		cleanup()
		if err != nil && h.writeAWSChunkedError(w, r, req.requestID, err) {
			return nil, "", func() {}, false
		}
		writeS3Error(w, http.StatusBadRequest, "IncompleteBody", "You did not provide the number of bytes specified by Content-Length.", r.URL.Path, req.requestID)
		return nil, "", func() {}, false
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		cleanup()
		writeS3Error(w, http.StatusServiceUnavailable, "ServiceUnavailable", "Gregale could not stage this upload.", r.URL.Path, req.requestID)
		return nil, "", func() {}, false
	}
	return file, base64.StdEncoding.EncodeToString(digest.Sum(nil)), cleanup, true
}
