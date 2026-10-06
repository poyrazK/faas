package objectstorage

import (
	"context"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func (p *S3) boundStreamClient(ctx context.Context, o *s3.Options) {
	if client, ok := p.client.Options().HTTPClient.(*http.Client); ok {
		streamClient := *client
		streamClient.Timeout = objectStreamTimeout(ctx)
		o.HTTPClient = &streamClient
	}
}
