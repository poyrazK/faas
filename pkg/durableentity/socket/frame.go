package socket

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

func decodeFrame(body []byte, frame *Frame) error {
	if len(body) > MaxFrameBytes || !utf8.Valid(body) {
		return errors.New("invalid frame bytes")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(frame) != nil || decoder.Decode(new(any)) != io.EOF || frame.ProtocolVersion != 1 || strings.TrimSpace(frame.RequestID) == "" || len(frame.RequestID) > api.MaxDurableEntityIdentityBytes || strings.ContainsFunc(frame.RequestID, unicode.IsControl) || strings.HasPrefix(frame.RequestID, "__gregale_alarm/") || len(frame.Payload) == 0 {
		return errors.New("invalid session frame")
	}
	return nil
}
