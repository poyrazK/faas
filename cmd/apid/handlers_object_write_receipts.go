package main

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) writeReceiptStore(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ObjectBucket, state.ObjectWriteReceiptStore, bool) {
	w.Header().Set("Cache-Control", "no-store")
	b, _, ok := s.loadBucketRecord(w, r, acct)
	if !ok || !s.authorizeBucketData(w, r, b, state.ObjectBucketPermissionWrite) {
		return b, nil, false
	}
	st, ok := s.store.(state.ObjectWriteReceiptStore)
	if !ok {
		bucketProblem(w, objectstorage.ErrUnavailable)
	}
	return b, st, ok
}

func (s *server) getObjectWriteReceipt(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, st, ok := s.writeReceiptStore(w, r, acct)
	if !ok {
		return
	}
	id := r.PathValue("receipt")
	if parsed, err := uuid.Parse(id); err != nil || parsed.String() != id {
		bucketProblem(w, state.ErrNotFound)
		return
	}
	c, err := st.GetObjectWriteReceipt(r.Context(), acct.ID, b.AppID, b.ID, id)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	if c.Status == "pending" {
		w.Header().Set("Retry-After", strconv.Itoa(int(api.ObjectUploadRecoveryRetry.Seconds())))
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *server) listObjectWriteReceipts(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, st, ok := s.writeReceiptStore(w, r, acct)
	if !ok {
		return
	}
	status, limit, cursor, valid := writeReceiptPageQuery(r)
	if !valid {
		bucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	page, err := st.ListObjectWriteReceipts(r.Context(), acct.ID, b.AppID, b.ID, status, limit, cursor)
	if errors.Is(err, state.ErrConflict) {
		err = objectstorage.ErrInvalid
	}
	if err != nil {
		bucketProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func writeReceiptPageQuery(r *http.Request) (string, int, string, bool) {
	q := r.URL.Query()
	for name, values := range q {
		if len(values) != 1 || name != "status" && name != "limit" && name != "cursor" {
			return "", 0, "", false
		}
	}
	limit := api.ObjectWriteReceiptPageDefault
	if q.Has("limit") {
		parsed, err := strconv.Atoi(q.Get("limit"))
		if err != nil || parsed < 1 {
			return "", 0, "", false
		}
		limit = parsed
	}
	status, limit, valid := api.ParseObjectWriteReceiptPage(q.Get("status"), limit, q.Get("cursor"))
	return status, limit, q.Get("cursor"), valid
}
