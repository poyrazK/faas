package profileproto

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
	"google.golang.org/protobuf/encoding/protowire"
)

// EpochUpload carries the epoch and process discriminator retained by the
// local bridge. Tenant and deployment selectors are discarded. Process IDs
// distinguish collectors within a VM; their raw values are not exported.
type EpochUpload struct {
	Epoch  string
	Upload Upload
}

// DecodePush reads Pyroscope's unary push.v1.PushRequest used by the Python
// SDK. It bounds decompression before protobuf traversal. Parsing occurs in
// guest-init inside the tenant VM, never in root vmmd.
func DecodePush(body []byte, compressed bool) ([]EpochUpload, error) {
	if len(body) == 0 || len(body) > api.ProfileMaxCompressedBytes {
		return nil, fmt.Errorf("push size outside bounds")
	}
	if compressed {
		gz, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		defer func() { _ = gz.Close() }()
		body, err = io.ReadAll(io.LimitReader(gz, api.ProfileMaxExpandedBytes+1))
		if err != nil {
			return nil, err
		}
		if len(body) > api.ProfileMaxExpandedBytes {
			return nil, fmt.Errorf("expanded push exceeds bounds")
		}
	}
	series, err := messages(body, 1)
	if err != nil {
		return nil, err
	}
	var out []EpochUpload
	for _, raw := range series {
		labels, err := messages(raw, 1)
		if err != nil {
			return nil, err
		}
		epoch, processID := "", ""
		for _, label := range labels {
			names, err := messages(label, 1)
			if err != nil {
				return nil, err
			}
			values, err := messages(label, 2)
			if err != nil {
				return nil, err
			}
			if len(names) == 1 && string(names[0]) == "gregale_epoch" && len(values) == 1 {
				if epoch != "" {
					return nil, fmt.Errorf("duplicate epoch")
				}
				epoch = string(values[0])
			}
			if len(names) == 1 && string(names[0]) == "gregale_process" && len(values) == 1 {
				processID = string(values[0])
			}
		}
		samples, err := messages(raw, 2)
		if err != nil {
			return nil, err
		}
		for _, sample := range samples {
			profiles, err := messages(sample, 1)
			if err != nil || len(profiles) != 1 {
				return nil, fmt.Errorf("invalid raw sample")
			}
			if len(out) >= api.ProfileMaxConcurrentUploads {
				return nil, fmt.Errorf("too many push samples")
			}
			out = append(out, EpochUpload{Epoch: epoch, Upload: Upload{Profile: profiles[0], ProcessID: processID}})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("push has no samples")
	}
	return out, nil
}

func messages(body []byte, want protowire.Number) ([][]byte, error) {
	var out [][]byte
	for len(body) > 0 {
		num, typ, n := protowire.ConsumeTag(body)
		if n < 0 {
			return nil, fmt.Errorf("invalid push tag")
		}
		body = body[n:]
		if num == want {
			if typ != protowire.BytesType {
				return nil, fmt.Errorf("invalid push field")
			}
			value, n := protowire.ConsumeBytes(body)
			if n < 0 {
				return nil, fmt.Errorf("invalid push message")
			}
			body = body[n:]
			if len(out) >= api.ProfileMaxNodes {
				return nil, fmt.Errorf("too many push fields")
			}
			out = append(out, value)
		} else {
			n := protowire.ConsumeFieldValue(num, typ, body)
			if n < 0 {
				return nil, fmt.Errorf("invalid push field")
			}
			body = body[n:]
		}
	}
	return out, nil
}
