package objectstorage

import (
	"context"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
)

// Explicit capability: other adapters cannot silently ignore a checksum policy.
type MultipartChecksumProvider interface {
	SupportsMultipartChecksum(objectchecksum.Policy) bool
}

func (p *S3) SupportsMultipartChecksum(policy objectchecksum.Policy) bool { return policy.Valid() }

func checksumValue(algorithm string, crc32, crc32c, crc64, sha1, sha256 *string) string {
	switch algorithm {
	case "CRC32":
		return aws.ToString(crc32)
	case "CRC32C":
		return aws.ToString(crc32c)
	case "CRC64NVME":
		return aws.ToString(crc64)
	case "SHA1":
		return aws.ToString(sha1)
	case "SHA256":
		return aws.ToString(sha256)
	}
	return ""
}

func setPartChecksum(in *s3.UploadPartInput, d objectchecksum.Digest) {
	in.ChecksumAlgorithm = types.ChecksumAlgorithm(d.Algorithm)
	switch d.Algorithm {
	case "CRC32":
		in.ChecksumCRC32 = aws.String(d.Value)
	case "CRC32C":
		in.ChecksumCRC32C = aws.String(d.Value)
	case "CRC64NVME":
		in.ChecksumCRC64NVME = aws.String(d.Value)
	case "SHA1":
		in.ChecksumSHA1 = aws.String(d.Value)
	case "SHA256":
		in.ChecksumSHA256 = aws.String(d.Value)
	}
}

func setCompletedPartChecksum(in *types.CompletedPart, algorithm, value string) {
	switch algorithm {
	case "CRC32":
		in.ChecksumCRC32 = aws.String(value)
	case "CRC32C":
		in.ChecksumCRC32C = aws.String(value)
	case "CRC64NVME":
		in.ChecksumCRC64NVME = aws.String(value)
	case "SHA1":
		in.ChecksumSHA1 = aws.String(value)
	case "SHA256":
		in.ChecksumSHA256 = aws.String(value)
	}
}

func setCompletionChecksum(in *s3.CompleteMultipartUploadInput, d objectchecksum.Digest) {
	in.ChecksumType = types.ChecksumType(d.Type)
	switch d.Algorithm {
	case "CRC32":
		in.ChecksumCRC32 = aws.String(d.Value)
	case "CRC32C":
		in.ChecksumCRC32C = aws.String(d.Value)
	case "CRC64NVME":
		in.ChecksumCRC64NVME = aws.String(d.Value)
	case "SHA1":
		in.ChecksumSHA1 = aws.String(d.Value)
	case "SHA256":
		in.ChecksumSHA256 = aws.String(d.Value)
	}
}

func checkedCompletionDigest(s objectchecksum.Snapshot, kind string, crc32, crc32c, crc64, sha1, sha256 *string) (objectchecksum.Digest, error) {
	if s.Empty() {
		return objectchecksum.Digest{}, nil
	}
	d := objectchecksum.Digest{Policy: objectchecksum.Policy{Algorithm: s.Algorithm, Type: kind}, Value: checksumValue(s.Algorithm, crc32, crc32c, crc64, sha1, sha256)}
	if !s.Complete() || !d.Valid(len(s.Parts)) || d != s.Digest {
		return objectchecksum.Digest{}, ErrUnavailable
	}
	return d, nil
}

func (p *S3) confirmMultipartChecksum(ctx context.Context, bucket string, r MultipartCompleteRequest, result UploadResult) (objectchecksum.Digest, error) {
	if r.Checksum.Empty() {
		return objectchecksum.Digest{}, nil
	}
	if err := multipartBeforeRequest(ctx, r); err != nil {
		return objectchecksum.Digest{}, err
	}
	in := &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(r.Key), ChecksumMode: types.ChecksumModeEnabled}
	if result.ProviderVersionID != "" {
		in.VersionId = aws.String(result.ProviderVersionID)
	}
	out, err := p.client.HeadObject(ctx, in, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	if err != nil {
		return objectchecksum.Digest{}, normalize(err)
	}
	if out == nil || aws.ToBool(out.DeleteMarker) || out.ContentLength == nil || *out.ContentLength != r.SizeBytes ||
		aws.ToString(out.ETag) != result.ETag || out.Metadata[ReservedMultipartSessionMetadataKey] != r.SessionID ||
		!validTrackedProofHeaders(out.ResultMetadata, ReservedMultipartSessionMetadataKey) ||
		!validCopySnapshotVersion(out.ResultMetadata, aws.ToString(out.VersionId), result.ProviderVersionID) {
		return objectchecksum.Digest{}, ErrUnavailable
	}
	return checkedCompletionDigest(r.Checksum, string(out.ChecksumType), out.ChecksumCRC32, out.ChecksumCRC32C, out.ChecksumCRC64NVME, out.ChecksumSHA1, out.ChecksumSHA256)
}

func ChecksumPartCount(value string) int {
	index := strings.LastIndexByte(value, '-')
	if index < 0 {
		return 0
	}
	n, _ := strconv.Atoi(value[index+1:])
	return n
}
