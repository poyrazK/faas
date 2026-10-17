package main

import (
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// imageDigestRequiredProblem explains a --image that is not digest-pinned.
// The title used to read "Image required" although an image was supplied,
// and nothing said how to obtain the digest (hunt #10, H10-118).
func imageDigestRequiredProblem(ref string) *api.Problem {
	return api.NewProblem(http.StatusBadRequest, api.CodeImageRequired, "Image digest required",
		fmt.Sprintf("image %q is not digest-pinned; deploy repository@sha256:<digest> instead. Resolve the digest with `docker buildx imagetools inspect %s` or `crane digest %s`.", ref, ref, ref))
}
