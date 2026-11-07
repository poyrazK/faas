package dbarchive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

const (
	s3PartSize    int64 = 64 << 20
	s3MaxParts    int64 = 10_000
	s3MaxPartSize int64 = 5 << 30
)

// putMultipart streams seekable sections sequentially. SDK retries can
// rewind one part without buffering a backup that may be terabytes long.
func (r *S3Remote) putMultipart(ctx context.Context, key string, body io.ReadSeeker, size int64) (err error) {
	partSize := max(s3PartSize, (size-1)/s3MaxParts+1)
	if partSize > s3MaxPartSize {
		return errors.New("dbarchive: object exceeds S3 multipart limits")
	}
	start, err := body.Seek(0, io.SeekCurrent)
	if err != nil {
		return fmt.Errorf("dbarchive: seek upload %s: %w", key, err)
	}
	upload, err := r.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(r.bucket), Key: aws.String(key), ChecksumAlgorithm: types.ChecksumAlgorithmCrc32,
	})
	if err != nil {
		return fmt.Errorf("dbarchive: start multipart upload %s: %w", key, err)
	}
	if aws.ToString(upload.UploadId) == "" {
		return errors.New("dbarchive: multipart upload returned no upload ID")
	}
	defer func() {
		if err == nil {
			return
		}
		// Failed and cancelled uploads must not leave billable parts behind.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if _, abortErr := r.client.AbortMultipartUpload(cleanup, &s3.AbortMultipartUploadInput{
			Bucket: aws.String(r.bucket), Key: aws.String(key), UploadId: upload.UploadId,
		}); abortErr != nil {
			err = errors.Join(err, fmt.Errorf("dbarchive: abort multipart upload %s: %w", key, abortErr))
		}
	}()
	source := &seekReaderAt{body: body}
	var parts []types.CompletedPart
	for offset := int64(0); offset < size; offset += partSize {
		number := int32(len(parts) + 1) // at most s3MaxParts
		length := min(partSize, size-offset)
		part, partErr := r.client.UploadPart(ctx, &s3.UploadPartInput{
			Bucket: aws.String(r.bucket), Key: aws.String(key), UploadId: upload.UploadId,
			PartNumber: aws.Int32(number), ContentLength: aws.Int64(length),
			Body: io.NewSectionReader(source, start+offset, length), ChecksumAlgorithm: types.ChecksumAlgorithmCrc32,
		})
		if partErr != nil {
			return fmt.Errorf("dbarchive: upload %s part %d: %w", key, number, partErr)
		}
		if aws.ToString(part.ETag) == "" {
			return fmt.Errorf("dbarchive: upload %s part %d returned no ETag", key, number)
		}
		parts = append(parts, types.CompletedPart{PartNumber: aws.Int32(number), ETag: part.ETag, ChecksumCRC32: part.ChecksumCRC32})
	}
	_, err = r.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket: aws.String(r.bucket), Key: aws.String(key), UploadId: upload.UploadId,
		MultipartUpload: &types.CompletedMultipartUpload{Parts: parts},
	})
	if err != nil {
		return fmt.Errorf("dbarchive: complete multipart upload %s: %w", key, err)
	}
	return nil
}

// seekReaderAt adapts any Remote.Put source to bounded, rewindable parts.
type seekReaderAt struct {
	mu   sync.Mutex
	body io.ReadSeeker
}

func (r *seekReaderAt) ReadAt(p []byte, offset int64) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.body.Seek(offset, io.SeekStart); err != nil {
		return 0, err
	}
	n, err := io.ReadFull(r.body, p)
	if errors.Is(err, io.ErrUnexpectedEOF) {
		err = io.EOF
	}
	return n, err
}
