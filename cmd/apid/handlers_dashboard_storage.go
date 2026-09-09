package main

// Dashboard surface for per-app object storage (issue #1397 / G10). Reads
// project the existing bucket, object, signed-URL, and storage-usage APIs;
// every state-changing form delegates to the same JSON handler used by the
// API so validation, ownership, accounting, and RFC 7807 errors cannot drift.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	dashboardStorageAction      = "app_storage_mutation"
	dashboardStorageCSRFCookie  = "faas_csrf_app_storage"
	dashboardStorageObjectLimit = 100
)

// parseAppStoragePath recognizes the per-app storage page. A trailing slash
// is accepted to match the other dashboard app subpages.
func parseAppStoragePath(rest string) (string, bool) {
	rest = strings.TrimSuffix(rest, "/")
	const suffix = "/storage"
	if !strings.HasSuffix(rest, suffix) {
		return "", false
	}
	slug := strings.TrimSuffix(rest, suffix)
	if slug == "" || strings.Contains(slug, "/") {
		return "", false
	}
	return slug, true
}

func (s *server) renderAppStorage(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug string) {
	s.renderAppStoragePage(w, r, log, acct, slug, nil)
}

func (s *server) renderAppStoragePage(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug string, signed *dashboard.StorageSignedURLPageItem) {
	ctx := r.Context()
	app, err := s.store.AppBySlug(ctx, slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}

	data := dashboard.StorageData{
		App:        dashboard.AppListItem{Slug: app.Slug, Status: string(app.Status), URL: appURLForDomain(app.Slug, s.domain)},
		Configured: s.objectStorage != nil,
		Enabled:    s.objectStorageEnabled(),
		Action:     dashboardStorageActionFlash(r),
		SignedURL:  signed,
	}
	if s.objectStorage != nil {
		data.Regions, data.DefaultRegion = s.objectStorage.Regions(), s.objectStorage.DefaultRegion
		data.MaxUploadBytes, data.MaxBucketsPerApp = s.objectStorage.MaxUploadBytes, s.objectStorage.MaxBucketsPerApp
	}

	if st, ok := s.store.(state.ObjectBucketStore); ok {
		rows, listErr := st.ListObjectBuckets(ctx, acct.ID, app.ID)
		if listErr != nil {
			data.ErrorMessage = "Storage data is temporarily unavailable. Please try again shortly."
			log.Warn("dashboard storage: list buckets", "account_id", acct.ID, "app_id", app.ID, "err", listErr)
		} else {
			data.Buckets = projectDashboardStorageBuckets(rows)
			data.SelectedBucketID = r.URL.Query().Get("bucket")
			if data.SelectedBucketID == "" {
				for _, row := range rows {
					if row.State == "ready" {
						data.SelectedBucketID = row.ID
						break
					}
				}
			}
			data.Prefix, data.Cursor = r.URL.Query().Get("prefix"), r.URL.Query().Get("cursor")
			if data.SelectedBucketID != "" && data.Enabled {
				objects, next, objectErr := s.dashboardStorageObjects(ctx, acct.ID, app.ID, data.SelectedBucketID, data.Prefix, data.Cursor)
				if objectErr != nil {
					data.ErrorMessage = "Object listing is temporarily unavailable. Please try again shortly."
					log.Warn("dashboard storage: list objects", "account_id", acct.ID, "app_id", app.ID, "bucket_id", data.SelectedBucketID, "err", objectErr)
				} else {
					data.Objects, data.NextCursor = objects, next
				}
			}
		}
	} else {
		data.ErrorMessage = "Storage metadata is temporarily unavailable. Please try again shortly."
	}

	day := timeNow().UTC()
	day = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	if rows, usageErr := s.store.StorageUsage(ctx, acct.ID, day); usageErr != nil {
		log.Warn("dashboard storage: usage", "account_id", acct.ID, "app_id", app.ID, "err", usageErr)
	} else {
		for _, row := range rows {
			if row.AppID == app.ID {
				data.Usage = &dashboard.StorageUsagePageItem{Day: row.Day.UTC().Format("2006-01-02"), SnapshotBytes: row.SnapshotBytes, LayerBytes: row.LayerBytes, TotalBytes: row.SnapshotBytes + row.LayerBytes}
				break
			}
		}
	}

	if s.sessions != nil {
		if token, tokenErr := middleware.IssueForAuthenticatedNamed(s.sessions, dashboardStorageAction, acct.ID, dashboardStorageCSRFCookie); tokenErr != nil {
			log.Warn("dashboard storage: issue csrf", "account_id", acct.ID, "app_id", app.ID, "err", tokenErr)
		} else {
			data.ActionCSRF = token
			http.SetCookie(w, &http.Cookie{Name: dashboardStorageCSRFCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
		}
	}

	appCount, countErr := s.store.CountDeployedApps(ctx, acct.ID)
	if countErr != nil {
		log.Warn("dashboard storage: count apps", "account_id", acct.ID, "err", countErr)
	}
	view, _ := AccountFrom(ctx)
	page := dashboard.Page{Title: "Storage — " + app.Slug, Body: "storage", Account: dashboardAccountView(view, appCount), Data: data}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(ctx), page); err != nil {
		renderProblem(w, log, err)
	}
}

func projectDashboardStorageBuckets(rows []state.ObjectBucket) []dashboard.StorageBucketPageItem {
	items := make([]dashboard.StorageBucketPageItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, dashboard.StorageBucketPageItem{ID: row.ID, Name: row.Name, Scope: row.Scope, Region: row.Region, State: row.State, CreatedAt: dashboardJobsTime(row.CreatedAt)})
	}
	return items
}

func (s *server) dashboardStorageObjects(ctx context.Context, accountID, appID, bucketID, prefix, cursor string) ([]dashboard.StorageObjectPageItem, string, error) {
	st, ok := s.store.(state.ObjectBucketStore)
	if !ok {
		return nil, "", state.ErrNotFound
	}
	bucket, err := st.GetObjectBucket(ctx, accountID, appID, bucketID)
	if err != nil {
		return nil, "", err
	}
	if bucket.State != "ready" || s.objectStorage == nil {
		return nil, "", objectstorage.ErrUnavailable
	}
	backend, err := s.objectStorage.Resolve(bucket.BackendID, bucket.BackendFingerprint)
	if err != nil {
		return nil, "", err
	}
	page, err := backend.Provider.ListObjects(ctx, bucket.PhysicalName, prefix, cursor, dashboardStorageObjectLimit)
	if err != nil {
		return nil, "", err
	}
	items := make([]dashboard.StorageObjectPageItem, 0, len(page.Items))
	for _, object := range page.Items {
		items = append(items, dashboard.StorageObjectPageItem{Key: object.Key, SizeBytes: object.Size, LastModified: dashboardJobsTime(object.LastModified)})
	}
	return items, page.NextCursor, nil
}

func dashboardStorageActionFlash(r *http.Request) string {
	switch r.URL.Query().Get("action") {
	case "created", "deleted", "object-deleted", "signed":
		return r.URL.Query().Get("action")
	case "error":
		return "error"
	default:
		return ""
	}
}

func (s *server) verifyDashboardStorageCSRF(w http.ResponseWriter, r *http.Request, accountID string) bool {
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardStorageAction, accountID, dashboardStorageCSRFCookie); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid CSRF token", "please reload the page and try again"))
		return false
	}
	return true
}

func (s *server) dashboardCreateStorageBucket(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.verifyDashboardStorageCSRF(w, r, acct.ID) {
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("could not parse storage form"))
		return
	}
	req := createBucketRequest{Name: strings.TrimSpace(r.FormValue("name")), Scope: strings.TrimSpace(r.FormValue("scope")), Region: strings.TrimSpace(r.FormValue("region"))}
	slug := r.PathValue("slug")
	resp := s.forwardDashboardStorageJSON(r, acct, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/buckets", "", url.Values{}, req, s.createBucket)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/storage?action=created", http.StatusSeeOther)
}

func (s *server) dashboardDeleteStorageBucket(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.verifyDashboardStorageCSRF(w, r, acct.ID) {
		return
	}
	if s.objectStorage == nil {
		bucketProblem(w, objectstorage.ErrUnavailable)
		return
	}
	slug, bucketID := r.PathValue("slug"), r.PathValue("bucket")
	resp := s.forwardDashboardStorageJSON(r, acct, http.MethodDelete, "/v1/apps/"+url.PathEscape(slug)+"/buckets/"+url.PathEscape(bucketID), bucketID, url.Values{}, nil, s.deleteBucket)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/storage?action=deleted", http.StatusSeeOther)
}

func (s *server) dashboardDeleteStorageObject(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.verifyDashboardStorageCSRF(w, r, acct.ID) {
		return
	}
	if s.objectStorage == nil {
		bucketProblem(w, objectstorage.ErrUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("could not parse object form"))
		return
	}
	slug, bucketID, key := r.PathValue("slug"), strings.TrimSpace(r.FormValue("bucket_id")), r.FormValue("key")
	query := url.Values{"key": []string{key}}
	path := "/v1/apps/" + url.PathEscape(slug) + "/buckets/" + url.PathEscape(bucketID) + "/objects"
	resp := s.forwardDashboardStorageJSON(r, acct, http.MethodDelete, path, bucketID, query, nil, s.deleteBucketObject)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/storage?bucket="+url.QueryEscape(bucketID)+"&action=object-deleted", http.StatusSeeOther)
}

func (s *server) dashboardSignStorageObject(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.verifyDashboardStorageCSRF(w, r, acct.ID) {
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("could not parse signed URL form"))
		return
	}
	req := api.ObjectSignRequest{Method: strings.ToUpper(strings.TrimSpace(r.FormValue("method"))), Key: r.FormValue("key"), ContentType: strings.TrimSpace(r.FormValue("content_type"))}
	if raw := strings.TrimSpace(r.FormValue("expires_in")); raw != "" {
		expires, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid expiry", "expires_in must be an integer"))
			return
		}
		req.ExpiresIn = expires
	}
	if raw := strings.TrimSpace(r.FormValue("size_bytes")); raw != "" {
		size, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid size", "size_bytes must be an integer"))
			return
		}
		req.SizeBytes = &size
	}
	slug, bucketID := r.PathValue("slug"), strings.TrimSpace(r.FormValue("bucket_id"))
	path := "/v1/apps/" + url.PathEscape(slug) + "/buckets/" + url.PathEscape(bucketID) + "/signed-url"
	resp := s.forwardDashboardStorageJSON(r, acct, http.MethodPost, path, bucketID, url.Values{}, req, s.signBucketObject)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	var signed api.ObjectSignedRequest
	if err := json.Unmarshal(resp.Body.Bytes(), &signed); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read signed URL response"))
		return
	}
	pageReq := r.Clone(r.Context())
	pageURL := *r.URL
	values := pageURL.Query()
	values.Set("bucket", bucketID)
	pageURL.Path = "/dashboard/apps/" + url.PathEscape(slug) + "/storage"
	pageURL.RawPath = ""
	pageURL.RawQuery = values.Encode()
	pageReq.URL = &pageURL
	s.renderAppStoragePage(w, pageReq, s.log, acct, slug, &dashboard.StorageSignedURLPageItem{URL: signed.URL, Method: signed.Method, ExpiresAt: dashboardJobsTime(signed.ExpiresAt)})
}

func (s *server) forwardDashboardStorageJSON(r *http.Request, acct state.Account, method, path, bucketID string, query url.Values, body any, handler dashboardJSONHandler) *httptest.ResponseRecorder {
	payload := []byte{}
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	req := r.Clone(r.Context())
	req.Method = method
	req.URL = cloneDashboardURL(r.URL, path, "")
	req.URL.RawQuery = query.Encode()
	req.Body = io.NopCloser(bytes.NewReader(payload))
	req.ContentLength = int64(len(payload))
	req.Header = r.Header.Clone()
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("slug", r.PathValue("slug"))
	req.SetPathValue("bucket", bucketID)
	resp := httptest.NewRecorder()
	handler(resp, req, acct)
	return resp
}
