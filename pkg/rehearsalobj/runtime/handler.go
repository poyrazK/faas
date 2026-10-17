package runtime

import (
	"context"
	"crypto/md5" // #nosec G501 -- S3 Content-MD5 is a wire integrity check.
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"hash/crc32"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/rehearsalobj"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type Handler struct {
	Catalog    rehearsalobj.Catalog
	Resolve    func(context.Context, string) (string, string, error)
	OpenSecret func([]byte) (string, error)
	Collector  *Collector
}

type objectCaller struct {
	resource                      rehearsalobj.Resource
	app, deployment, key, payload string
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	caller := h.authenticate(w, r)
	if caller == nil {
		return
	}
	id, ok := h.admit(w, r, caller)
	if !ok {
		return
	}
	h.executeRequest(w, r, caller, id)
}
func (h *Handler) resolveCaller(w http.ResponseWriter, r *http.Request) *objectCaller {
	if h.Collector == nil {
		objectError(w, 503, "ServiceUnavailable")
		return nil
	}
	app, deployment, e := h.Resolve(r.Context(), r.RemoteAddr)
	if e != nil || app == "" || deployment == "" {
		if e != nil {
			h.Collector.Fail()
		}
		objectError(w, 403, "AccessDenied")
		return nil
	}
	resource, e := h.Catalog.ScenarioObjectsForApp(r.Context(), app)
	if e != nil {
		if !errors.Is(e, state.ErrNotFound) {
			h.Collector.Fail()
		}
		objectError(w, 403, "AccessDenied")
		return nil
	}
	if resource.CollectorID != h.Collector.ID || h.Collector.failed.Load() {
		h.failAuthentication(r.Context(), resource, app)
		objectError(w, 503, "ServiceUnavailable")
		return nil
	}
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
	key := ""
	if len(parts) == 2 {
		key = parts[1]
	}
	return &objectCaller{resource: resource, app: app, deployment: deployment, key: key}
}
func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) *objectCaller {
	c := h.resolveCaller(w, r)
	if c == nil {
		return nil
	}
	secret, e := h.OpenSecret(c.resource.SecretSealed)
	if e != nil {
		h.Collector.Fail()
		h.failAuthentication(r.Context(), c.resource, c.app)
		objectError(w, 503, "ServiceUnavailable")
		return nil
	}
	access := c.resource.AccessKey
	if c.app != c.resource.AppID {
		access, secret = rehearsalwork.ObjectCredentials(secret, c.app)
	}
	if c.resource.CallerDeploymentID != "" && c.deployment != c.resource.CallerDeploymentID {
		h.failAuthentication(r.Context(), c.resource, c.app)
		objectError(w, 403, "AccessDenied")
		return nil
	}
	c.payload, e = s3gateway.VerifyHeaderSignature(r.Context(), r, access, secret, rehearsalobj.Region, time.Now())
	if e != nil {
		h.failAuthentication(r.Context(), c.resource, c.app)
		objectError(w, 403, "SignatureDoesNotMatch")
		return nil
	}
	freshApp, freshDeployment, e := h.Resolve(r.Context(), r.RemoteAddr)
	if e != nil || freshApp != c.app || freshDeployment != c.deployment {
		if e != nil {
			h.Collector.Fail()
		}
		h.failAuthentication(r.Context(), c.resource, c.app)
		objectError(w, 403, "AccessDenied")
		return nil
	}
	return c
}
func (h *Handler) admit(w http.ResponseWriter, r *http.Request, c *objectCaller) (string, bool) {
	id := uuid.NewString()
	admitted := false
	_, e := h.Catalog.UpdateScenarioObjects(r.Context(), c.resource.AccountID, c.resource.RunID, c.app, func(v *rehearsalobj.Resource) error {
		if v.ID != c.resource.ID || v.CollectorID != h.Collector.ID || v.CollectorClosed {
			return state.ErrConflict
		}
		if len(v.Attempts) >= rehearsalobj.MaxRequests {
			v.Failed = true
			return nil
		}
		method, key := r.Method, c.key
		if method != "PUT" && method != "GET" && method != "HEAD" && method != "DELETE" {
			method = "UNSUPPORTED"
			v.Failed = true
		}
		if key != "" && !rehearsalobj.ValidKey(key) {
			key = ""
			v.Failed = true
		}
		v.Attempts = append(v.Attempts, rehearsalobj.Attempt{ID: id, AppID: c.app, Workload: c.resource.CallerWorkload, DeploymentID: c.deployment, Method: method, Key: key, Pending: true})
		admitted = true
		return nil
	})
	if e != nil || !admitted {
		if e != nil && !errors.Is(e, state.ErrConflict) {
			h.Collector.Fail()
		}
		objectError(w, 503, "ServiceUnavailable")
		return "", false
	}
	return id, true
}
func (h *Handler) executeRequest(w http.ResponseWriter, r *http.Request, c *objectCaller, id string) {
	// A failed abort update leaves a durable pending obligation, which cannot seal.
	finished := false
	defer func(parent context.Context) {
		if !finished {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 3*time.Second)
			defer cancel()
			if _, e := h.finish(ctx, c.resource, id, nil, true); e != nil && !errors.Is(e, state.ErrConflict) {
				h.Collector.Fail()
			}
		}
	}(r.Context())
	input, parseErr := parseObjectRequest(r, c.resource.Spec.Bucket, c.key, c.payload)
	if c.resource.CallerPermission == "read" && (r.Method == "PUT" || r.Method == "DELETE") {
		input = nil
		parseErr = errors.New("read-only rehearsal binding")
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancel()
	result, e := h.finish(ctx, c.resource, id, input, parseErr != nil || r.Context().Err() != nil)
	if e != nil {
		if !errors.Is(e, state.ErrConflict) {
			h.Collector.Fail()
		}
		objectError(w, 503, "ServiceUnavailable")
		return
	}
	finished = true
	if parseErr != nil {
		objectError(w, 501, "NotImplemented")
		return
	}
	if result.status >= 400 {
		code := "NoSuchKey"
		if result.status == 413 {
			code = "EntityTooLarge"
		}
		if result.status == 400 {
			code = "InvalidRequest"
		}
		objectError(w, result.status, code)
		return
	}
	writeObjectResult(w, r, input, result)
}
func (h *Handler) failAuthentication(ctx context.Context, r rehearsalobj.Resource, app string) {
	failedCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	_, e := h.Catalog.UpdateScenarioObjects(failedCtx, r.AccountID, r.RunID, app, func(v *rehearsalobj.Resource) error {
		if v.ID != r.ID {
			return state.ErrConflict
		}
		v.Failed = true
		return nil
	})
	if e != nil && !errors.Is(e, state.ErrConflict) {
		h.Collector.Fail()
	}
}

type objectInput struct {
	method, key, prefix string
	data                rehearsalobj.Data
}
type objectResult struct {
	status  int
	data    rehearsalobj.Data
	objects []rehearsalobj.Object
}

func parseObjectRequest(r *http.Request, bucket, key, payload string) (*objectInput, error) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
	if len(parts) == 0 || parts[0] != bucket || r.URL.RawPath != "" && r.URL.EscapedPath() != r.URL.RawPath { // checked below without path cleaning
		return nil, errors.New("unsupported bucket")
	}
	input := &objectInput{method: r.Method, key: key}
	query, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		return nil, e
	}
	if ids, ok := query["x-id"]; ok {
		expected := map[string]string{"PUT": "PutObject", "GET": "GetObject", "HEAD": "HeadObject", "DELETE": "DeleteObject"}[r.Method]
		if key == "" {
			switch r.Method {
			case "GET":
				expected = "ListObjectsV2"
			case "HEAD":
				expected = "HeadBucket"
			}
		}
		if len(ids) != 1 || ids[0] != expected {
			return nil, errors.New("unsupported S3 operation identity")
		}
		query.Del("x-id")
	}
	if key == "" {
		if r.Method == "GET" && query.Get("list-type") == "2" {
			for name, values := range query {
				if len(values) != 1 || (name != "list-type" && name != "prefix" && name != "max-keys") {
					return nil, errors.New("unsupported listing")
				}
			}
			if n := query.Get("max-keys"); n != "" {
				count, e := strconv.Atoi(n)
				if e != nil || count < rehearsalobj.MaxObjects || count > 1000 {
					return nil, errors.New("unsupported listing page size")
				}
			}
			input.method = "LIST"
			input.prefix = query.Get("prefix")
			if len(input.prefix) > 512 {
				return nil, errors.New("invalid listing prefix")
			}
		} else if r.Method == "HEAD" && len(query) == 0 {
			input.method = "BUCKET"
		} else {
			return nil, errors.New("unsupported bucket operation")
		}
	} else if !rehearsalobj.ValidKey(key) || len(query) > 0 || (r.Method != "PUT" && r.Method != "GET" && r.Method != "HEAD" && r.Method != "DELETE") {
		return nil, errors.New("unsupported object operation")
	}
	if e = validateObjectHeaders(r); e != nil {
		return nil, e
	}
	body, e := io.ReadAll(io.LimitReader(r.Body, rehearsalobj.MaxObjectBytes+1))
	if e != nil || len(body) > rehearsalobj.MaxObjectBytes || r.ContentLength >= 0 && r.ContentLength != int64(len(body)) {
		return nil, errors.New("incomplete object body")
	}
	if payload != "UNSIGNED-PAYLOAD" && payload != rehearsalobj.HashBytes(body) {
		return nil, errors.New("object payload hash mismatch")
	}
	if e = validateObjectChecksums(r, body); e != nil {
		return nil, e
	}
	if input.method != "PUT" && len(body) > 0 {
		return nil, errors.New("unexpected object body")
	}
	if input.method == "PUT" {
		contentType := r.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		metadata := map[string]string{}
		for name, values := range r.Header {
			if strings.HasPrefix(strings.ToLower(name), "x-amz-meta-") {
				if len(values) != 1 {
					return nil, errors.New("invalid object metadata")
				}
				metadata[strings.TrimPrefix(strings.ToLower(name), "x-amz-meta-")] = values[0]
			}
		}
		input.data = rehearsalobj.Data{Key: key, Body: body, ContentType: contentType, Metadata: metadata}
		if _, e = rehearsalobj.Snapshot([]rehearsalobj.Data{input.data}); e != nil {
			return nil, e
		}
	}
	return input, nil
}

func validateObjectHeaders(r *http.Request) error {
	for _, name := range []string{"Range", "If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since", "Content-Encoding", "Content-Disposition", "Content-Language", "Cache-Control", "Expires", "Trailer"} {
		if r.Header.Get(name) != "" {
			return errors.New("unsupported object header")
		}
	}
	for name, values := range r.Header {
		name = strings.ToLower(name)
		if strings.HasPrefix(name, "x-amz-") && !strings.HasPrefix(name, "x-amz-meta-") {
			switch name {
			case "x-amz-date", "x-amz-content-sha256", "x-amz-checksum-sha256", "x-amz-checksum-crc32", "x-amz-checksum-crc32c", "x-amz-sdk-checksum-algorithm":
			default:
				return errors.New("unsupported S3 feature")
			}
		}
		if (strings.HasPrefix(name, "x-amz-") || name == "content-type" || name == "content-md5") && len(values) != 1 {
			return errors.New("ambiguous object header")
		}
	}
	return nil
}
func validateObjectChecksums(r *http.Request, body []byte) error {
	if v := r.Header.Get("Content-MD5"); v != "" {
		sum := md5.Sum(body) // #nosec G401 -- S3 Content-MD5 wire integrity, never authentication.
		if v != base64.StdEncoding.EncodeToString(sum[:]) {
			return errors.New("object checksum mismatch")
		}
	}
	for header, polynomial := range map[string]uint32{"X-Amz-Checksum-Crc32": crc32.IEEE, "X-Amz-Checksum-Crc32c": crc32.Castagnoli} {
		if v := r.Header.Get(header); v != "" {
			sum := crc32.Checksum(body, crc32.MakeTable(polynomial))
			b := make([]byte, 4)
			binary.BigEndian.PutUint32(b, sum)
			if v != base64.StdEncoding.EncodeToString(b) {
				return errors.New("object checksum mismatch")
			}
		}
	}
	if v := r.Header.Get("X-Amz-Checksum-Sha256"); v != "" {
		sum, e := base64.StdEncoding.DecodeString(v)
		if e != nil || len(sum) != 32 || hex.EncodeToString(sum) != rehearsalobj.HashBytes(body) {
			return errors.New("object checksum mismatch")
		}
	}
	if algorithm := r.Header.Get("X-Amz-Sdk-Checksum-Algorithm"); algorithm != "" {
		names := map[string]string{"CRC32": "X-Amz-Checksum-Crc32", "CRC32C": "X-Amz-Checksum-Crc32c", "SHA256": "X-Amz-Checksum-Sha256"}
		name, ok := names[algorithm]
		if !ok || r.Header.Get(name) == "" {
			return errors.New("unsupported checksum algorithm")
		}
	}
	return nil
}

func (h *Handler) finish(ctx context.Context, resource rehearsalobj.Resource, id string, input *objectInput, failed bool) (objectResult, error) {
	result := objectResult{status: 400}
	_, e := h.Catalog.UpdateScenarioObjects(ctx, resource.AccountID, resource.RunID, "", func(v *rehearsalobj.Resource) error {
		if v.ID != resource.ID || v.CollectorID != h.Collector.ID || v.CollectorClosed || (v.Status != "ready" && v.Status != "sealing") || !v.ExpiresAt.After(time.Now()) {
			return state.ErrConflict
		}
		index := -1
		for i, a := range v.Attempts {
			if a.ID == id && a.Pending {
				index = i
				break
			}
		}
		if index < 0 {
			return state.ErrConflict
		}
		v.Attempts[index].Pending = false
		if failed || input == nil {
			v.Failed = true
			return nil
		}
		result = applyObjectInput(v, input)
		if result.status == 413 {
			v.Failed = true
			return nil
		}
		if input.method == "PUT" || input.method == "DELETE" {
			sequence := 1
			for _, a := range v.Attempts {
				if a.Mutation != nil {
					sequence++
				}
			}
			m := &rehearsalobj.Mutation{Workload: v.Attempts[index].Workload, Sequence: sequence, Method: input.method, Key: input.key, Status: result.status}
			if input.method == "PUT" {
				objects, _ := rehearsalobj.Snapshot([]rehearsalobj.Data{input.data})
				m.Object = &objects[0]
			}
			v.Attempts[index].Mutation = m
		}
		return nil
	})
	return result, e
}
func applyObjectInput(v *rehearsalobj.Resource, input *objectInput) objectResult {
	result := objectResult{status: 200}
	index := -1
	for i, d := range v.Current {
		if d.Key == input.key {
			index = i
			break
		}
	}
	switch input.method {
	case "PUT":
		current := append([]rehearsalobj.Data{}, v.Current...)
		if index < 0 {
			current = append(current, input.data)
		} else {
			current[index] = input.data
		}
		if _, e := rehearsalobj.Snapshot(current); e != nil {
			result.status = 413
			return result
		}
		v.Current = current
		result.data = input.data
	case "DELETE":
		if index >= 0 {
			v.Current = append(v.Current[:index], v.Current[index+1:]...)
		}
		result.status = 204
	case "GET", "HEAD":
		if index < 0 {
			result.status = 404
		} else {
			result.data = v.Current[index]
		}
	case "LIST":
		objects, _ := rehearsalobj.Snapshot(v.Current)
		for _, o := range objects {
			if strings.HasPrefix(o.Key, input.prefix) {
				result.objects = append(result.objects, o)
			}
		}
	}
	return result
}
func writeObjectResult(w http.ResponseWriter, r *http.Request, input *objectInput, result objectResult) {
	switch input.method {
	case "PUT", "GET", "HEAD":
		d := result.data
		sum := md5.Sum(d.Body) // #nosec G401 -- S3 ETag compatibility, never identity or authentication.
		w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
		if input.method != "PUT" {
			w.Header().Set("Content-Type", d.ContentType)
			w.Header().Set("Content-Length", strconv.Itoa(len(d.Body)))
			for k, v := range d.Metadata {
				w.Header().Set("X-Amz-Meta-"+k, v)
			}
		}
	case "LIST":
		writeObjectList(w, input, result.objects)
		return
	}
	w.WriteHeader(result.status)
	if r.Method == "GET" {
		_, _ = w.Write(result.data.Body)
	}
}

type listObject struct {
	Key  string `xml:"Key"`
	Size int64  `xml:"Size"`
}

func writeObjectList(w http.ResponseWriter, input *objectInput, objects []rehearsalobj.Object) {
	list := struct {
		XMLName     xml.Name     `xml:"ListBucketResult"`
		Xmlns       string       `xml:"xmlns,attr"`
		IsTruncated bool         `xml:"IsTruncated"`
		KeyCount    int          `xml:"KeyCount"`
		Prefix      string       `xml:"Prefix"`
		Contents    []listObject `xml:"Contents"`
	}{Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/", KeyCount: len(objects), Prefix: input.prefix}
	sort.Slice(objects, func(i, j int) bool { return objects[i].Key < objects[j].Key })
	for _, o := range objects {
		list.Contents = append(list.Contents, listObject{o.Key, o.Size})
	}
	w.Header().Set("Content-Type", "application/xml")
	_ = xml.NewEncoder(w).Encode(list)
}
func objectError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_ = xml.NewEncoder(w).Encode(struct {
		XMLName xml.Name `xml:"Error"`
		Code    string   `xml:"Code"`
	}{Code: code})
}
