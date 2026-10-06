package objectstorage

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/onebox-faas/faas/pkg/api"
)

func validMultipartUploadID(id string) bool {
	if strings.TrimSpace(id) == "" || len(id) > api.ObjectProviderUploadIDMaxBytes || !utf8.ValidString(id) {
		return false
	}
	for _, c := range id {
		if c < 32 || c == 127 {
			return false
		}
	}
	return true
}

func (p *S3) discoverMultipartUpload(ctx context.Context, bucket string, r MultipartCreateRequest) (string, error) {
	found, keyMarker, uploadMarker := "", "", ""
	seen := map[string]bool{}
	for range api.ObjectMultipartInitiationMaxPages {
		if r.BeforeRequest != nil {
			if err := r.BeforeRequest(ctx); err != nil {
				return "", err
			}
		}
		out, err := p.client.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{
			Bucket: aws.String(bucket), Prefix: aws.String(r.Key), MaxUploads: aws.Int32(api.MaxObjectS3ListItems),
			KeyMarker: stringPtrOrNil(keyMarker), UploadIdMarker: stringPtrOrNil(uploadMarker), EncodingType: types.EncodingTypeUrl,
		})
		if err != nil {
			return "", normalize(err)
		}
		if out == nil || out.IsTruncated == nil || len(out.Uploads) > api.MaxObjectS3ListItems || len(out.CommonPrefixes) != 0 {
			return "", ErrUnavailable
		}
		last := keyMarker
		for _, u := range out.Uploads {
			key, err := historyResponseKey(aws.ToString(u.Key), out.EncodingType)
			id := aws.ToString(u.UploadId)
			identity := key + "\x00" + id
			if err != nil || !strings.HasPrefix(key, r.Key) || key < last || !validMultipartUploadID(id) || seen[identity] {
				return "", ErrUnavailable
			}
			last, seen[identity] = key, true
			if key == r.Key {
				if found != "" {
					return "", ErrConflict
				}
				found = id
			}
		}
		if !aws.ToBool(out.IsTruncated) {
			return found, nil
		}
		key, err := historyResponseKey(aws.ToString(out.NextKeyMarker), out.EncodingType)
		id := aws.ToString(out.NextUploadIdMarker)
		if err != nil || len(out.Uploads) == 0 || key != last || !validMultipartUploadID(id) || id != aws.ToString(out.Uploads[len(out.Uploads)-1].UploadId) || key == keyMarker && id == uploadMarker {
			return "", ErrUnavailable
		}
		keyMarker, uploadMarker = key, id
	}
	return "", ErrUnavailable
}
