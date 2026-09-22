package s3gateway

import (
	"bufio"
	"crypto/hmac"
	"crypto/md5"  // #nosec G501 -- Content-MD5 is required for S3 wire compatibility.
	"crypto/sha1" // #nosec G505 -- SHA-1 is required for S3 wire checksum compatibility.
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"hash"
	"hash/crc32"
	"hash/crc64"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const (
	maxAWSChunkHeaderBytes = 8 << 10
	maxAWSTrailerBytes     = 16 << 10
	crc64NVMEPolynomial    = 0x9a6c9329ac4bc9b5
)

type awsChunkedError struct {
	status  int
	code    string
	message string
}

func contentSHA256Mismatch() error {
	return &awsChunkedError{status: http.StatusBadRequest, code: "XAmzContentSHA256Mismatch", message: "The provided x-amz-content-sha256 does not match the request body."}
}

func (e *awsChunkedError) Error() string { return e.code }

func invalidAWSChunked(message string) error {
	return &awsChunkedError{status: http.StatusBadRequest, code: "InvalidRequest", message: message}
}

func badAWSChunkSignature() error {
	return &awsChunkedError{status: http.StatusForbidden, code: "SignatureDoesNotMatch", message: "The request signature we calculated does not match the signature you provided."}
}

func badAWSChunkDigest() error {
	return &awsChunkedError{status: http.StatusBadRequest, code: "BadDigest", message: "The checksum you specified did not match what Gregale received."}
}

// prepareAWSChunkedBody replaces the encoded request body with a bounded
// plaintext reader after the seed request signature has been verified.
func prepareAWSChunkedBody(r *http.Request, parsed sigV4Request, secret, region string, maxDecoded int64) (*awsChunkedReader, error) {
	if !isStreamingPayloadHash(parsed.PayloadHash) {
		return nil, nil
	}
	decodedLength, err := strconv.ParseInt(r.Header.Get("X-Amz-Decoded-Content-Length"), 10, 64)
	if err != nil || decodedLength < 0 || decodedLength > maxDecoded {
		return nil, invalidAWSChunked("x-amz-decoded-content-length is missing or invalid.")
	}
	encoding, ok := stripAWSChunkedEncoding(r.Header.Get("Content-Encoding"))
	if !ok {
		return nil, invalidAWSChunked("Content-Encoding must end with aws-chunked.")
	}

	mode := parsed.PayloadHash
	trailerName := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Amz-Trailer")))
	hasTrailer := mode == streamingSignedPayloadTrailer || mode == streamingUnsignedTrailer
	if hasTrailer {
		if strings.Contains(trailerName, ",") || newAWSChecksum(trailerName) == nil {
			return nil, invalidAWSChunked("x-amz-trailer must name one supported checksum trailer.")
		}
	} else if trailerName != "" {
		return nil, invalidAWSChunked("x-amz-trailer is only valid for a trailer payload.")
	}

	reader := &awsChunkedReader{
		body:          bufio.NewReaderSize(r.Body, maxAWSChunkHeaderBytes),
		closer:        r.Body,
		mode:          mode,
		decodedLength: decodedLength,
		previousSig:   parsed.Signature,
		timestamp:     parsed.SignedAt.UTC().Format("20060102T150405Z"),
		scope:         parsed.ScopeDate + "/" + region + "/s3/aws4_request",
		trailerName:   trailerName,
		checksum:      newAWSChecksum(trailerName),
	}
	if mode != streamingUnsignedTrailer {
		reader.signingKey = deriveSigV4Key(secret, parsed.ScopeDate, region, "s3")
	}
	r.Body = reader
	r.ContentLength = decodedLength
	r.Header.Set("Content-Length", strconv.FormatInt(decodedLength, 10))
	if encoding == "" {
		r.Header.Del("Content-Encoding")
	} else {
		r.Header.Set("Content-Encoding", encoding)
	}
	return reader, nil
}

func stripAWSChunkedEncoding(value string) (string, bool) {
	parts := strings.Split(value, ",")
	if len(parts) == 0 || !strings.EqualFold(strings.TrimSpace(parts[len(parts)-1]), "aws-chunked") {
		return "", false
	}
	out := make([]string, 0, len(parts)-1)
	for _, part := range parts[:len(parts)-1] {
		part = strings.TrimSpace(part)
		if part == "" || strings.EqualFold(part, "aws-chunked") {
			return "", false
		}
		out = append(out, part)
	}
	return strings.Join(out, ", "), true
}

type awsChunkedReader struct {
	body          *bufio.Reader
	closer        io.Closer
	mode          string
	decodedLength int64
	decoded       int64
	remaining     int64
	previousSig   string
	expectedSig   string
	timestamp     string
	scope         string
	signingKey    []byte
	trailerName   string
	checksum      hash.Hash
	chunkHash     hash.Hash
	chunkActive   bool
	done          bool
	err           error
}

func (r *awsChunkedReader) Close() error { return r.closer.Close() }

func (r *awsChunkedReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.err != nil {
		return 0, r.err
	}
	if r.done {
		return 0, io.EOF
	}
	for r.remaining == 0 {
		if r.chunkActive {
			if err := r.finishChunk(); err != nil {
				r.err = err
				return 0, err
			}
		}
		if err := r.startChunk(); err != nil {
			if errors.Is(err, io.EOF) {
				r.done = true
				return 0, io.EOF
			}
			r.err = err
			return 0, err
		}
	}

	limit := int64(len(p))
	if limit > r.remaining {
		limit = r.remaining
	}
	n, err := io.ReadFull(r.body, p[:limit])
	if err != nil {
		r.err = invalidAWSChunked("The aws-chunked body ended before the declared chunk size.")
		return n, r.err
	}
	r.remaining -= int64(n)
	r.decoded += int64(n)
	if r.decoded > r.decodedLength {
		r.err = invalidAWSChunked("The decoded body exceeds x-amz-decoded-content-length.")
		return n, r.err
	}
	if r.checksum != nil {
		_, _ = r.checksum.Write(p[:n])
	}
	if r.chunkHash != nil {
		_, _ = r.chunkHash.Write(p[:n])
	}
	// A multipart provider transport stops reading after the decoded
	// Content-Length. Verify the terminating zero frame and any checksum
	// trailer before releasing the final plaintext bytes to that transport.
	if r.remaining == 0 && r.decoded == r.decodedLength {
		if err := r.finishChunk(); err != nil {
			r.err = err
			return 0, err
		}
		if err := r.startChunk(); err != nil {
			if errors.Is(err, io.EOF) {
				r.done = true
				return n, nil
			}
			r.err = err
			return 0, err
		}
		r.err = invalidAWSChunked("The aws-chunked body has data beyond x-amz-decoded-content-length.")
		return 0, r.err
	}
	return n, nil
}

func (r *awsChunkedReader) startChunk() error {
	line, err := readAWSChunkLine(r.body, maxAWSChunkHeaderBytes)
	if err != nil {
		return invalidAWSChunked("The aws-chunked body has an invalid chunk header.")
	}
	sizeText := line
	signature := ""
	if r.mode != streamingUnsignedTrailer {
		parts := strings.Split(line, ";")
		if len(parts) != 2 || !strings.HasPrefix(parts[1], "chunk-signature=") {
			return invalidAWSChunked("A signed aws-chunked frame is missing its chunk signature.")
		}
		sizeText = parts[0]
		signature = strings.TrimPrefix(parts[1], "chunk-signature=")
		decoded, decodeErr := hex.DecodeString(signature)
		if decodeErr != nil || len(decoded) != sha256.Size || signature != strings.ToLower(signature) {
			return invalidAWSChunked("An aws-chunked frame has an invalid chunk signature.")
		}
	}
	if sizeText == "" || strings.IndexFunc(sizeText, func(r rune) bool {
		return r < '0' || r > '9' && r < 'A' || r > 'F' && r < 'a' || r > 'f'
	}) != -1 {
		return invalidAWSChunked("An aws-chunked frame has an invalid chunk size.")
	}
	size, err := strconv.ParseInt(sizeText, 16, 64)
	if err != nil || size < 0 || size > r.decodedLength-r.decoded {
		return invalidAWSChunked("An aws-chunked frame has an invalid chunk size.")
	}
	if size == 0 {
		if signature != "" && !r.verifyChunkSignature(signature, nil) {
			return badAWSChunkSignature()
		}
		if signature != "" {
			r.previousSig = signature
		}
		if r.decoded != r.decodedLength {
			return invalidAWSChunked("The decoded body length does not match x-amz-decoded-content-length.")
		}
		if r.trailerName != "" {
			if err := r.readTrailers(); err != nil {
				return err
			}
		} else {
			line, err = readAWSChunkLine(r.body, maxAWSChunkHeaderBytes)
			if err != nil || line != "" {
				return invalidAWSChunked("The aws-chunked body has an invalid terminator.")
			}
		}
		if _, err = r.body.ReadByte(); !errors.Is(err, io.EOF) {
			return invalidAWSChunked("The aws-chunked body contains data after its terminator.")
		}
		return io.EOF
	}
	r.remaining = size
	r.expectedSig = signature
	r.chunkActive = true
	if signature != "" {
		r.chunkHash = sha256.New()
	}
	return nil
}

func (r *awsChunkedReader) finishChunk() error {
	terminator := make([]byte, 2)
	if _, err := io.ReadFull(r.body, terminator); err != nil || string(terminator) != "\r\n" {
		return invalidAWSChunked("An aws-chunked frame is missing its terminator.")
	}
	// The data hash is accumulated independently for a signed frame so Read can
	// remain streaming. Re-read is avoided by keeping a per-frame hash below.
	if r.expectedSig != "" {
		if !r.verifyChunkSignature(r.expectedSig, r.chunkHash.Sum(nil)) {
			return badAWSChunkSignature()
		}
		r.previousSig = r.expectedSig
	}
	r.expectedSig = ""
	r.chunkHash = nil
	r.chunkActive = false
	return nil
}

func (r *awsChunkedReader) verifyChunkSignature(signature string, digest []byte) bool {
	if digest == nil {
		empty := sha256.Sum256(nil)
		digest = empty[:]
	}
	empty := sha256.Sum256(nil)
	stringToSign := "AWS4-HMAC-SHA256-PAYLOAD\n" + r.timestamp + "\n" + r.scope + "\n" + r.previousSig + "\n" + hex.EncodeToString(empty[:]) + "\n" + hex.EncodeToString(digest)
	calculated := hex.EncodeToString(hmacSHA256(r.signingKey, stringToSign))
	return subtle.ConstantTimeCompare([]byte(calculated), []byte(signature)) == 1
}

func (r *awsChunkedReader) readTrailers() error {
	trailers := map[string]string{}
	total := 0
	for {
		line, err := readAWSChunkLine(r.body, maxAWSChunkHeaderBytes)
		if err != nil {
			return invalidAWSChunked("The aws-chunked body has invalid trailers.")
		}
		total += len(line) + 2
		if total > maxAWSTrailerBytes {
			return invalidAWSChunked("The aws-chunked trailers are too large.")
		}
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		name = strings.ToLower(strings.TrimSpace(name))
		value = strings.TrimSpace(value)
		if !ok || name == "" || value == "" || trailers[name] != "" {
			return invalidAWSChunked("The aws-chunked body has invalid trailers.")
		}
		trailers[name] = value
	}
	checksumValue := trailers[r.trailerName]
	if checksumValue == "" || len(trailers) != mapTrailerCount(r.mode) {
		return invalidAWSChunked("The declared checksum trailer is missing.")
	}
	expected, err := base64.StdEncoding.DecodeString(checksumValue)
	if err != nil || subtle.ConstantTimeCompare(expected, r.checksum.Sum(nil)) != 1 {
		return badAWSChunkDigest()
	}
	if r.mode == streamingSignedPayloadTrailer {
		trailerSignature := trailers["x-amz-trailer-signature"]
		decoded, decodeErr := hex.DecodeString(trailerSignature)
		if decodeErr != nil || len(decoded) != sha256.Size || trailerSignature != strings.ToLower(trailerSignature) {
			return invalidAWSChunked("The aws-chunked trailer signature is invalid.")
		}
		canonical := r.trailerName + ":" + checksumValue + "\n"
		digest := sha256.Sum256([]byte(canonical))
		stringToSign := "AWS4-HMAC-SHA256-TRAILER\n" + r.timestamp + "\n" + r.scope + "\n" + r.previousSig + "\n" + hex.EncodeToString(digest[:])
		calculated := hex.EncodeToString(hmacSHA256(r.signingKey, stringToSign))
		if subtle.ConstantTimeCompare([]byte(calculated), []byte(trailerSignature)) != 1 {
			return badAWSChunkSignature()
		}
	}
	return nil
}

func mapTrailerCount(mode string) int {
	if mode == streamingSignedPayloadTrailer {
		return 2
	}
	return 1
}

func readAWSChunkLine(reader *bufio.Reader, limit int) (string, error) {
	line, err := reader.ReadSlice('\n')
	if err != nil || len(line) > limit || !strings.HasSuffix(string(line), "\r\n") {
		return "", errors.New("invalid aws-chunked line")
	}
	return strings.TrimSuffix(string(line), "\r\n"), nil
}

func deriveSigV4Key(secret, date, region, service string) []byte {
	dateKey := hmacSHA256([]byte("AWS4"+secret), date)
	regionKey := hmacSHA256(dateKey, region)
	serviceKey := hmacSHA256(regionKey, service)
	return hmacSHA256(serviceKey, "aws4_request")
}

func hmacSHA256(key []byte, value string) []byte {
	digest := hmac.New(sha256.New, key)
	_, _ = digest.Write([]byte(value))
	return digest.Sum(nil)
}

func newAWSChecksum(name string) hash.Hash {
	switch strings.ToLower(name) {
	case "x-amz-checksum-crc32":
		return crc32.NewIEEE()
	case "x-amz-checksum-crc32c":
		return crc32.New(crc32.MakeTable(crc32.Castagnoli))
	case "x-amz-checksum-crc64nvme":
		return crc64.New(crc64.MakeTable(crc64NVMEPolynomial))
	case "x-amz-checksum-sha1":
		return sha1.New() // #nosec G401 -- S3 wire compatibility.
	case "x-amz-checksum-sha256":
		return sha256.New()
	default:
		return nil
	}
}

var awsChecksumHeaderNames = []string{
	"x-amz-checksum-crc32",
	"x-amz-checksum-crc32c",
	"x-amz-checksum-crc64nvme",
	"x-amz-checksum-sha1",
	"x-amz-checksum-sha256",
}

func requestChecksum(header http.Header) (hash.Hash, []byte, error) {
	var selected string
	for _, name := range awsChecksumHeaderNames {
		if value := header.Get(name); value != "" {
			if selected != "" {
				return nil, nil, invalidAWSChunked("Only one x-amz-checksum header may be supplied.")
			}
			selected = name
		}
	}
	if selected == "" {
		return nil, nil, nil
	}
	expected, err := base64.StdEncoding.DecodeString(header.Get(selected))
	digest := newAWSChecksum(selected)
	if err != nil || digest == nil || len(expected) != digest.Size() {
		return nil, nil, invalidAWSChunked("An x-amz-checksum header is invalid.")
	}
	return digest, expected, nil
}

func verifyRequestChecksum(header http.Header, body []byte) error {
	digest, expected, err := requestChecksum(header)
	if err != nil || digest == nil {
		return err
	}
	_, _ = digest.Write(body)
	if subtle.ConstantTimeCompare(expected, digest.Sum(nil)) != 1 {
		return badAWSChunkDigest()
	}
	return nil
}

type requestIntegrityReader struct {
	body             io.ReadCloser
	remaining        int64
	payloadHash      hash.Hash
	expectedPayload  []byte
	md5Hash          hash.Hash
	expectedMD5      []byte
	checksum         hash.Hash
	expectedChecksum []byte
	err              error
}

func newRequestIntegrityReader(body io.ReadCloser, size int64, payloadHash string, header http.Header) (*requestIntegrityReader, error) {
	reader := &requestIntegrityReader{body: body, remaining: size}
	if payloadHash != "UNSIGNED-PAYLOAD" && !isStreamingPayloadHash(payloadHash) {
		expected, err := hex.DecodeString(payloadHash)
		if err != nil || len(expected) != sha256.Size {
			return nil, invalidAWSChunked("x-amz-content-sha256 is invalid.")
		}
		reader.payloadHash, reader.expectedPayload = sha256.New(), expected
	}
	if value := header.Get("Content-MD5"); value != "" {
		expected, err := base64.StdEncoding.DecodeString(value)
		if err != nil || len(expected) != 16 {
			return nil, invalidAWSChunked("Content-MD5 is invalid.")
		}
		reader.md5Hash, reader.expectedMD5 = md5.New(), expected // #nosec G401 -- S3 wire compatibility.
	}
	checksum, expected, err := requestChecksum(header)
	if err != nil {
		return nil, err
	}
	reader.checksum, reader.expectedChecksum = checksum, expected
	return reader, nil
}

func (r *requestIntegrityReader) Close() error { return r.body.Close() }

func (r *requestIntegrityReader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.body.Read(p)
	if n > 0 {
		for _, digest := range []hash.Hash{r.payloadHash, r.md5Hash, r.checksum} {
			if digest != nil {
				_, _ = digest.Write(p[:n])
			}
		}
		r.remaining -= int64(n)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		r.err = err
		return 0, err
	}
	if r.remaining != 0 {
		if errors.Is(err, io.EOF) {
			r.err = invalidAWSChunked("The request body ended before Content-Length.")
			return 0, r.err
		}
		return n, nil
	}
	if r.payloadHash != nil && subtle.ConstantTimeCompare(r.expectedPayload, r.payloadHash.Sum(nil)) != 1 {
		r.err = contentSHA256Mismatch()
		return 0, r.err
	}
	if r.md5Hash != nil && subtle.ConstantTimeCompare(r.expectedMD5, r.md5Hash.Sum(nil)) != 1 {
		r.err = badAWSChunkDigest()
		return 0, r.err
	}
	if r.checksum != nil && subtle.ConstantTimeCompare(r.expectedChecksum, r.checksum.Sum(nil)) != 1 {
		r.err = badAWSChunkDigest()
		return 0, r.err
	}
	return n, nil
}
