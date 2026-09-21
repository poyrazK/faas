package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const requestBodyMemoryThreshold int64 = 1 << 20

// admittedFileBody owns a request spool file. Closing the forwarded request
// body releases the descriptor and removes the file on every proxy exit path.
type admittedFileBody struct {
	*os.File
	path string
}

func (b *admittedFileBody) Close() error {
	err := b.File.Close()
	removeErr := os.Remove(b.path)
	if err != nil {
		return err
	}
	if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return removeErr
	}
	return nil
}

// admitRequestBody receives the complete bounded request body before wake and
// capacity admission. Bodies up to 1 MiB stay in memory; larger bodies spill
// to an owner-cleaned temporary file so Scale's 250 MiB contract does not turn
// into equivalent gateway heap pressure.
//
// This phase has its own size-derived total deadline and a sliding idle read
// deadline. The app execution budget is stamped only after this function and
// platform wake/capacity selection have completed.
func admitRequestBody(w http.ResponseWriter, r *http.Request, app App) bool {
	limit := app.Plan.MaxRequestBodyBytes()
	allowance := app.Plan.RequestUploadTimeout()
	if r.ContentLength >= 0 && r.ContentLength <= limit {
		allowance = api.RequestUploadTimeoutForBytes(r.ContentLength)
	}
	return admitRequestBodyWithin(w, r, limit, allowance)
}

func admitRequestBodyWithin(w http.ResponseWriter, r *http.Request, limit int64, allowance time.Duration) bool {
	if r.Body == nil || r.Body == http.NoBody || isUpgradeRequest(r) {
		return false
	}

	original := r.Body
	expectedLength := r.ContentLength
	uploadCtx, cancel := context.WithTimeout(r.Context(), allowance)
	defer cancel()
	reader, stopReader := newCtxReader(uploadCtx, original)
	defer stopReader()

	controller := http.NewResponseController(w)
	defer func() { _ = controller.SetReadDeadline(time.Time{}) }()

	var memory bytes.Buffer
	if r.ContentLength > 0 && r.ContentLength <= requestBodyMemoryThreshold {
		memory.Grow(int(r.ContentLength))
	}
	var spool *os.File
	cleanupSpool := func() {
		if spool != nil {
			path := spool.Name()
			_ = spool.Close()
			_ = os.Remove(path)
		}
	}

	var total int64
	buf := make([]byte, 32*1024)
	for {
		readDeadline := time.Now().Add(api.RequestUploadIdleTimeout)
		if totalDeadline, ok := uploadCtx.Deadline(); ok && totalDeadline.Before(readDeadline) {
			readDeadline = totalDeadline
		}
		_ = controller.SetReadDeadline(readDeadline)

		n, readErr := reader.Read(buf)
		if n > 0 {
			total += int64(n)
			if total > limit {
				cleanupSpool()
				_ = original.Close()
				api.WriteProblem(w, api.ErrRequestBodyTooLarge(limit, total))
				return true
			}
			if spool == nil && total > requestBodyMemoryThreshold {
				var err error
				spool, err = os.CreateTemp("", "gregale-request-body-*")
				if err != nil {
					_ = original.Close()
					api.WriteProblem(w, api.ErrInternal("request body admission storage is unavailable"))
					return true
				}
				if _, err = spool.Write(memory.Bytes()); err != nil {
					cleanupSpool()
					_ = original.Close()
					api.WriteProblem(w, api.ErrInternal("request body admission storage is unavailable"))
					return true
				}
				memory.Reset()
			}
			var writeErr error
			if spool != nil {
				_, writeErr = spool.Write(buf[:n])
			} else {
				_, writeErr = memory.Write(buf[:n])
			}
			if writeErr != nil {
				cleanupSpool()
				_ = original.Close()
				api.WriteProblem(w, api.ErrInternal("request body admission storage is unavailable"))
				return true
			}
		}
		if readErr == nil {
			continue
		}
		if errors.Is(readErr, io.EOF) {
			if expectedLength >= 0 && total != expectedLength {
				cleanupSpool()
				_ = original.Close()
				api.WriteProblem(w, api.ErrRequestBodyReadFailed())
				return true
			}
			break
		}

		cleanupSpool()
		_ = original.Close()
		var maxErr *http.MaxBytesError
		if errors.As(readErr, &maxErr) {
			api.WriteProblem(w, api.ErrRequestBodyTooLarge(maxErr.Limit, maxErr.Limit+1))
			return true
		}
		var netErr net.Error
		if errors.Is(uploadCtx.Err(), context.DeadlineExceeded) || (errors.As(readErr, &netErr) && netErr.Timeout()) {
			api.WriteProblem(w, api.ErrRequestUploadTimeout(allowance))
			return true
		}
		api.WriteProblem(w, api.ErrRequestBodyReadFailed())
		return true
	}

	_ = original.Close()
	// GetBody makes the admitted body replayable (ADR-201 §1). Both backing
	// stores are already rewindable, so retry costs no extra buffering and no
	// extra memory ceiling: these bytes are resident either way and were
	// already counted against MaxRequestBodyBytes above.
	//
	// Ownership: GetBody deliberately hands out NON-owning readers. The spool
	// file is deleted by admittedFileBody.Close(), and a proxy attempt closes
	// whatever it is given — so if a replay handed out an owning body, the
	// first attempt's Close would delete the file out from under the second.
	// The retry loop (retryingProxy) therefore keeps the owning body and
	// closes it exactly once; every attempt, including the first, proxies a
	// NopCloser. When retry is disabled nothing calls GetBody and r.Body stays
	// the owning body, which is byte-identical to the pre-ADR-201 path.
	if spool != nil {
		if _, err := spool.Seek(0, io.SeekStart); err != nil {
			cleanupSpool()
			api.WriteProblem(w, api.ErrInternal("request body admission storage is unavailable"))
			return true
		}
		r.Body = &admittedFileBody{File: spool, path: spool.Name()}
		r.GetBody = func() (io.ReadCloser, error) {
			if _, err := spool.Seek(0, io.SeekStart); err != nil {
				return nil, err
			}
			return io.NopCloser(spool), nil
		}
	} else {
		body := append([]byte(nil), memory.Bytes()...)
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
	}
	r.ContentLength = total
	r.TransferEncoding = nil
	return false
}
