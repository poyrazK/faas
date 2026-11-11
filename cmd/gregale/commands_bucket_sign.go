package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const bucketSignUsage = "usage: gregale bucket sign <app> <bucket-id> <key> [--method GET|PUT] [--expires DURATION] [--size-bytes N] [--content-type TYPE]"

// maxBucketSignExpiry is the API's signed URL ceiling (docs/object-storage.md).
const maxBucketSignExpiry = 15 * time.Minute

// cmdBucketSign mints a signed URL for one object through
// POST /v1/apps/{app}/buckets/{bucket}/signed-url, so a browser or another
// service can move it without credentials or an S3 SDK.
func cmdBucketSign(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("bucket sign", flag.ContinueOnError)
	method := fs.String("method", http.MethodGet, "GET to download, PUT to upload")
	expires := fs.Duration("expires", 0, "URL lifetime, at most 15m (default 5m)")
	size := fs.Int64("size-bytes", -1, "exact upload size in bytes (required with --method PUT)")
	contentType := fs.String("content-type", "", "upload content type (with --method PUT)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 3 || !api.ValidAppSlug(positional[0]) || strings.TrimSpace(positional[1]) == "" || positional[2] == "" {
		PrintUsage(os.Stderr, bucketSignUsage, "bucket")
		return 1
	}
	request, err := bucketSignRequest(positional[2], *method, *expires, *size, *contentType)
	if err != nil {
		printCommandValidation(os.Stderr, "%v\n", err)
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	signed, err := client.SignBucketObject(context.Background(), positional[0], positional[1], request)
	if err != nil {
		return printErr("Could not sign the object request", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(signed))
	}
	renderBucketSigned(osStdout, signed)
	return 0
}

// bucketSignRequest checks the flags the API would refuse, with flag names.
func bucketSignRequest(key, method string, expires time.Duration, size int64, contentType string) (api.ObjectSignRequest, error) {
	request := api.ObjectSignRequest{Method: strings.ToUpper(strings.TrimSpace(method)), Key: key}
	switch request.Method {
	case http.MethodGet:
		if size >= 0 || contentType != "" {
			return request, fmt.Errorf("--size-bytes and --content-type apply to --method PUT")
		}
	case http.MethodPut:
		if size < 0 {
			return request, fmt.Errorf("--method PUT needs --size-bytes: the URL accepts a body of exactly that size")
		}
		request.SizeBytes = &size
		request.ContentType = contentType
	default:
		return request, fmt.Errorf("--method must be GET or PUT, not %q", method)
	}
	if expires < 0 || expires > maxBucketSignExpiry || expires%time.Second != 0 {
		return request, fmt.Errorf("--expires must be whole seconds up to 15m")
	}
	request.ExpiresIn = int64(expires / time.Second)
	return request, nil
}

func renderBucketSigned(w io.Writer, signed api.ObjectSignedRequest) {
	_, _ = fmt.Fprintf(w, "%s %s\n", signed.Method, signed.URL)
	_, _ = fmt.Fprintf(w, "Expires: %s\n", signed.ExpiresAt.UTC().Format(time.RFC3339))
	names := make([]string, 0, len(signed.Headers))
	for name := range signed.Headers {
		names = append(names, name)
	}
	sort.Strings(names)
	curl := []string{"curl", "-X", signed.Method}
	for _, name := range names {
		_, _ = fmt.Fprintf(w, "Header: %s: %s\n", name, signed.Headers[name])
		curl = append(curl, "-H", quoteCLIArg(name+": "+signed.Headers[name]))
	}
	if signed.Method == http.MethodPut {
		curl = append(curl, "--data-binary", "@FILE")
	}
	curl = append(curl, quoteCLIArg(signed.URL))
	_, _ = fmt.Fprintf(w, "Send it with exactly these headers and no Gregale credentials, e.g.:\n  %s\n", strings.Join(curl, " "))
}
