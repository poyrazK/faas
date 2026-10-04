package s3gateway

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"net/http"
)

func decodeObjectTags(w http.ResponseWriter, r *http.Request, payloadHash string) (map[string]string, error) {
	body, err := readVerifiedRequestBody(w, r, payloadHash, api.MaxObjectTaggingBodyBytes)
	if err != nil {
		return nil, err
	}
	return objectstorage.ParseObjectTaggingXML(body)
}
