package objectstorage

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

const (
	OwnedNodeAdmissionHeader       = "X-Gregale-Write-Admission"
	OwnedNodeAdmissionSizeHeader   = "X-Gregale-Write-Size"
	OwnedNodeAdmissionStatusHeader = "X-Gregale-Write-Admission-Status"
)

// ObjectWriteAdmissionChecker is an optional pre-dispatch hint. Its response
// never settles a receipt and must never classify an already dispatched write.
type ObjectWriteAdmissionChecker interface {
	CheckObjectWriteAdmission(context.Context, string, string, int64) error
}

var ErrWritePressure = errors.New("object storage is throttling new writes")

func (p *S3) CheckObjectWriteAdmission(ctx context.Context, bucket, kind string, size int64) error {
	if !p.ownedNodeReceipts {
		return nil
	}
	out, err := p.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)}, func(o *s3.Options) {
		o.RetryMaxAttempts = 1
		o.APIOptions = append(o.APIOptions, ownedAdmissionSelector(kind, size))
	})
	var headers http.Header
	if err != nil {
		var response *smithyhttp.ResponseError
		if errors.As(err, &response) && response.HTTPResponse() != nil && response.HTTPStatusCode() == http.StatusServiceUnavailable {
			headers = response.HTTPResponse().Header
			if len(headers.Values(OwnedNodeAdmissionStatusHeader)) == 1 && headers.Get(OwnedNodeAdmissionStatusHeader) == "blocked" {
				return ErrWritePressure
			}
		}
		return ErrUnavailable
	}
	if out == nil {
		return ErrUnavailable
	}
	response, ok := awsmiddleware.GetRawResponse(out.ResultMetadata).(*smithyhttp.Response)
	if !ok || response == nil || response.Response == nil {
		return ErrUnavailable
	}
	headers = response.Header
	// Older nodes have no hint. Their actual write gates and conservative
	// uncertain-outcome rules remain authoritative during a rolling upgrade.
	if len(headers.Values(OwnedNodeAdmissionStatusHeader)) == 0 {
		return nil
	}
	if len(headers.Values(OwnedNodeAdmissionStatusHeader)) != 1 || headers.Get(OwnedNodeAdmissionStatusHeader) != "ready" {
		return ErrUnavailable
	}
	return nil
}

func ownedAdmissionSelector(kind string, size int64) func(*middleware.Stack) error {
	return func(stack *middleware.Stack) error {
		return stack.Build.Add(middleware.BuildMiddlewareFunc("GregaleOwnedNodeAdmission", func(ctx context.Context, in middleware.BuildInput, next middleware.BuildHandler) (middleware.BuildOutput, middleware.Metadata, error) {
			r, ok := in.Request.(*smithyhttp.Request)
			if !ok {
				return middleware.BuildOutput{}, middleware.Metadata{}, ErrUnavailable
			}
			r.Header.Set(OwnedNodeAdmissionHeader, kind)
			r.Header.Set(OwnedNodeAdmissionSizeHeader, strconv.FormatInt(size, 10))
			return next.HandleBuild(ctx, in)
		}), middleware.After)
	}
}
