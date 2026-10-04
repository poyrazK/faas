package objectstorage

import (
	"bytes"
	"encoding/xml"
	"io"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/onebox-faas/faas/pkg/api"
)

// SDK error decoding alone may accept duplicate Code fields. A metadata
// cascade needs one complete, unambiguous NoSuchBucket observation.
type bucketDeleteProofClient struct {
	base    aws.HTTPClient
	missing *bool
}

func (c bucketDeleteProofClient) Do(request *http.Request) (*http.Response, error) {
	response, err := c.base.Do(request)
	if err != nil || response == nil || response.StatusCode != http.StatusNotFound {
		return response, err
	}
	if response.Body == nil {
		return nil, ErrUnavailable
	}
	body := response.Body
	data, err := io.ReadAll(io.LimitReader(body, api.MaxObjectProviderMetadataResponseBytes+1))
	_ = body.Close()
	if err != nil || int64(len(data)) > api.MaxObjectProviderMetadataResponseBytes {
		return nil, ErrUnavailable
	}
	var document struct {
		XMLName xml.Name `xml:"Error"`
		Codes   []string `xml:"Code"`
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	if decoder.Decode(&document) == nil && decoder.Decode(new(any)) == io.EOF && len(document.Codes) == 1 && document.Codes[0] == "NoSuchBucket" {
		*c.missing = true
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	return response, nil
}
