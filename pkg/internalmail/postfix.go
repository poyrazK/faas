package internalmail

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/textproto"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const (
	PostfixReportPath     = APIPath + "/reports/postfix"
	ReportSenderHeader    = "X-Gregale-Report-Sender"
	ReportRecipientHeader = "X-Gregale-Report-Recipient"
)

// ParsePostfixReport accepts only the RFC 3464 delivery notifications requested
// by our one-recipient SMTP submissions. It never infers complaints, inbox
// placement, or permission to resubmit. Raw MIME and provider diagnostics are
// discarded; only a bounded enhanced status code enters the event history.
func ParsePostfixReport(recipient string, raw []byte) (Report, error) {
	id, err := bounceRecipientID(recipient)
	if err != nil || len(raw) == 0 || len(raw) > api.InternalMailMaxDSNBytes {
		return Report{}, ErrInvalid
	}
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return Report{}, ErrInvalid
	}
	media, params, err := reportMediaType(textproto.MIMEHeader(message.Header))
	if err != nil || media != "multipart/report" || params["report-type"] != "delivery-status" || params["boundary"] == "" {
		return Report{}, ErrInvalid
	}
	status, err := deliveryStatusPart(multipart.NewReader(message.Body, params["boundary"]))
	if err != nil {
		return Report{}, ErrInvalid
	}
	report, err := decodeDeliveryStatus(id, status)
	if err != nil {
		return Report{}, err
	}
	// Postfix retries the same queued DSN bytes, including after a lost HTTP
	// response. A hash makes every retry use the same database event identity.
	digest := sha256.Sum256(raw)
	report.EventID = "postfix-dsn:" + hex.EncodeToString(digest[:])
	return report, nil
}

func bounceRecipientID(recipient string) (string, error) {
	address, err := parseAddress(recipient)
	if err != nil || address.Address != recipient {
		return "", ErrInvalid
	}
	local, _, _ := strings.Cut(recipient, "@")
	value, ok := strings.CutPrefix(local, "bounce+")
	id, err := uuid.Parse(value)
	if !ok || err != nil || id.String() != value {
		return "", ErrInvalid
	}
	return value, nil
}

func reportMediaType(h textproto.MIMEHeader) (string, map[string]string, error) {
	if len(h.Values("Content-Type")) != 1 {
		return "", nil, ErrInvalid
	}
	return mime.ParseMediaType(h.Get("Content-Type"))
}

func deliveryStatusPart(reader *multipart.Reader) ([]byte, error) {
	var status []byte
	for n := 0; ; n++ {
		part, err := reader.NextRawPart()
		if errors.Is(err, io.EOF) && (n == 2 || n == 3) {
			return status, nil
		}
		if err != nil || n >= 3 {
			return nil, ErrInvalid
		}
		media, _, err := reportMediaType(part.Header)
		if err != nil {
			return nil, ErrInvalid
		}
		switch n {
		case 0:
			if media != "text/plain" {
				return nil, ErrInvalid
			}
		case 1:
			encoding := strings.ToLower(part.Header.Get("Content-Transfer-Encoding"))
			if media != "message/delivery-status" || encoding != "" && encoding != "7bit" && encoding != "8bit" {
				return nil, ErrInvalid
			}
			status, err = io.ReadAll(io.LimitReader(part, api.InternalMailMaxDSNStatusBytes+1))
			if err != nil || len(status) > api.InternalMailMaxDSNStatusBytes {
				return nil, ErrInvalid
			}
		case 2:
			if media != "text/rfc822-headers" && media != "message/rfc822" {
				return nil, ErrInvalid
			}
		}
		if _, err := io.Copy(io.Discard, part); err != nil {
			return nil, ErrInvalid
		}
	}
}

func decodeDeliveryStatus(id string, raw []byte) (Report, error) {
	message, recipient, err := deliveryStatusHeaders(raw)
	if err != nil || singleField(message, "Original-Envelope-Id") != "gregale-"+id {
		return Report{}, ErrInvalid
	}
	return deliveryRecipient(id, recipient)
}

func deliveryStatusHeaders(raw []byte) (textproto.MIMEHeader, textproto.MIMEHeader, error) {
	reader := textproto.NewReader(bufio.NewReader(bytes.NewReader(raw)))
	message, err := reader.ReadMIMEHeader()
	if err != nil || singleField(message, "Reporting-Mta") == "" {
		return nil, nil, ErrInvalid
	}
	recipient, err := reader.ReadMIMEHeader()
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, nil, ErrInvalid
	}
	remaining, err := io.ReadAll(reader.R)
	if err != nil || len(bytes.TrimSpace(remaining)) != 0 {
		// maild submits exactly one recipient per envelope. Reject extra
		// blocks instead of applying a partially parsed multi-recipient DSN.
		return nil, nil, ErrInvalid
	}
	return message, recipient, nil
}

func deliveryRecipient(id string, recipient textproto.MIMEHeader) (Report, error) {
	typ, final, ok := strings.Cut(singleField(recipient, "Final-Recipient"), ";")
	if !ok || !strings.EqualFold(strings.TrimSpace(typ), "rfc822") {
		return Report{}, ErrInvalid
	}
	address, err := parseAddress(strings.TrimSpace(final))
	if err != nil {
		return Report{}, ErrInvalid
	}
	status := singleField(recipient, "Status")
	action := singleField(recipient, "Action")
	kind := deliveryKind(action, status)
	if kind == "" {
		return Report{}, ErrInvalid
	}
	return Report{RecipientID: id, RecipientAddress: address.Address, Kind: kind, Code: "dsn_" + status}, nil
}

func singleField(h textproto.MIMEHeader, name string) string {
	if len(h.Values(name)) != 1 {
		return ""
	}
	return h.Get(name)
}

func deliveryKind(action, status string) string {
	fields := strings.Split(status, ".")
	if len(fields) != 3 || len(fields[0]) != 1 {
		return ""
	}
	for _, field := range fields {
		if len(field) == 0 || len(field) > 3 {
			return ""
		}
		for _, digit := range field {
			if digit < '0' || digit > '9' {
				return ""
			}
		}
	}
	switch {
	case (action == "delivered" || action == "relayed") && fields[0] == "2":
		return "accepted"
	case action == "delayed" && fields[0] == "4":
		return "deferred"
	case action == "failed" && (fields[0] == "5" || fields[0] == "4"):
		// Postfix may expire its queue with the last temporary SMTP status.
		// Action=failed is terminal even when that status still starts with 4.
		return "bounced"
	default:
		return ""
	}
}
