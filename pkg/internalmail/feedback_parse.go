package internalmail

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/mail"
	"net/textproto"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type parsedFeedback struct {
	report   Report
	format   string
	from     string
	identity string
}

// parseFeedback accepts a strict one-recipient subset of RFC 3464 and RFC 5965.
// It does not authenticate anything. IngestFeedback verifies the original outer
// DKIM signature before the parsed observation can reach durable state.
func parseFeedback(raw []byte) (parsedFeedback, error) {
	if len(raw) == 0 || len(raw) > api.InternalMailMaxFeedbackBytes {
		return parsedFeedback{}, ErrInvalid
	}
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return parsedFeedback{}, ErrInvalid
	}
	header := textproto.MIMEHeader(message.Header)
	from, err := parseAddress(singleField(header, "From"))
	identity := singleField(header, "Message-Id")
	if err != nil || singleField(header, "MIME-Version") != "1.0" || !feedbackMessageID(identity) {
		return parsedFeedback{}, ErrInvalid
	}
	media, params, err := reportMediaType(header)
	if err != nil || media != "multipart/report" || params["boundary"] == "" {
		return parsedFeedback{}, ErrInvalid
	}
	format := ""
	switch params["report-type"] {
	case "delivery-status":
		format = "dsn"
	case "feedback-report":
		format = "arf"
	default:
		return parsedFeedback{}, ErrInvalid
	}
	status, original, err := feedbackParts(multipart.NewReader(message.Body, params["boundary"]), params["report-type"])
	if err != nil {
		return parsedFeedback{}, err
	}
	correlation, err := feedbackCorrelation(original)
	if err != nil {
		return parsedFeedback{}, err
	}
	report, err := feedbackObservation(format, status, correlation)
	return parsedFeedback{report: report, format: format, from: from.Address, identity: identity}, err
}

func feedbackMessageID(value string) bool {
	return len(value) >= 5 && len(value) <= api.InternalMailMaxHeaderBytes && strings.HasPrefix(value, "<") && strings.HasSuffix(value, ">") && strings.Count(value, "@") == 1 && !strings.ContainsAny(value, " \t\r\n\x00")
}

func feedbackParts(reader *multipart.Reader, reportType string) ([]byte, textproto.MIMEHeader, error) {
	var status []byte
	var original textproto.MIMEHeader
	for n := 0; ; n++ {
		part, err := reader.NextRawPart()
		if errors.Is(err, io.EOF) && n == 3 {
			return status, original, nil
		}
		if err != nil || n >= 3 {
			return nil, nil, ErrInvalid
		}
		media, _, err := reportMediaType(part.Header)
		encoding := strings.ToLower(part.Header.Get("Content-Transfer-Encoding"))
		if err != nil || len(part.Header.Values("Content-Transfer-Encoding")) > 1 || encoding != "" && encoding != "7bit" && encoding != "8bit" {
			return nil, nil, ErrInvalid
		}
		switch n {
		case 0:
			if media != "text/plain" {
				return nil, nil, ErrInvalid
			}
		case 1:
			if media != "message/"+reportType {
				return nil, nil, ErrInvalid
			}
			status, err = io.ReadAll(io.LimitReader(part, api.InternalMailMaxDSNStatusBytes+1))
			if err != nil || len(status) > api.InternalMailMaxDSNStatusBytes {
				return nil, nil, ErrInvalid
			}
		case 2:
			if media != "text/rfc822-headers" && media != "message/rfc822" {
				return nil, nil, ErrInvalid
			}
			original, err = feedbackOriginalHeaders(part)
			if err != nil {
				return nil, nil, err
			}
		}
		if _, err := io.Copy(io.Discard, part); err != nil {
			return nil, nil, ErrInvalid
		}
	}
}

func feedbackOriginalHeaders(input io.Reader) (textproto.MIMEHeader, error) {
	// A returned message body is neither retained nor parsed. Limit its header
	// block independently of the total outer report's bound.
	limited := &io.LimitedReader{R: input, N: api.InternalMailMaxDSNStatusBytes + 1}
	reader := textproto.NewReader(bufio.NewReader(limited))
	header, err := reader.ReadMIMEHeader()
	if err != nil && !errors.Is(err, io.EOF) || len(header) == 0 || limited.N == 0 {
		return nil, ErrInvalid
	}
	return header, nil
}

func feedbackCorrelation(header textproto.MIMEHeader) (Report, error) {
	messageID := singleField(header, "Message-Id")
	if !strings.HasPrefix(messageID, "<") || !strings.HasSuffix(messageID, ">") {
		return Report{}, ErrInvalid
	}
	id, domain, ok := strings.Cut(messageID[1:len(messageID)-1], "@")
	parsed, err := uuid.Parse(id)
	if !ok || err != nil || parsed.String() != id || !validDomain(domain) || domain != strings.ToLower(domain) || singleField(header, "X-Gregale-Mail-Recipient-Id") != id {
		return Report{}, ErrInvalid
	}
	to, err := parseAddress(singleField(header, "To"))
	if err != nil {
		return Report{}, ErrInvalid
	}
	from, err := parseAddress(singleField(header, "From"))
	if err != nil || !strings.HasSuffix(from.Address, "@"+domain) {
		return Report{}, ErrInvalid
	}
	return Report{RecipientID: id, RecipientAddress: to.Address, MessageDomain: domain}, nil
}

func feedbackObservation(format string, raw []byte, correlation Report) (Report, error) {
	if format == "arf" {
		return complaintObservation(raw, correlation)
	}
	message, recipient, err := deliveryStatusHeaders(raw)
	if err != nil {
		return Report{}, ErrInvalid
	}
	// Remote MTAs may omit ENVID; returned recipient-bound headers remain
	// mandatory. If supplied, ENVID must agree with those headers.
	if len(message.Values("Original-Envelope-Id")) != 0 && singleField(message, "Original-Envelope-Id") != "gregale-"+correlation.RecipientID {
		return Report{}, ErrInvalid
	}
	report, err := deliveryRecipient(correlation.RecipientID, recipient)
	if err != nil || report.RecipientAddress != correlation.RecipientAddress || report.Kind != "bounced" && report.Kind != "deferred" {
		return Report{}, ErrInvalid
	}
	if len(recipient.Values("Original-Recipient")) != 0 {
		typ, address, ok := strings.Cut(singleField(recipient, "Original-Recipient"), ";")
		if !ok || !strings.EqualFold(strings.TrimSpace(typ), "rfc822") || strings.TrimSpace(address) != correlation.RecipientAddress {
			return Report{}, ErrInvalid
		}
	}
	report.MessageDomain = correlation.MessageDomain
	return report, nil
}

func complaintObservation(raw []byte, correlation Report) (Report, error) {
	reader := textproto.NewReader(bufio.NewReader(bytes.NewReader(raw)))
	header, err := reader.ReadMIMEHeader()
	if err != nil && !errors.Is(err, io.EOF) {
		return Report{}, ErrInvalid
	}
	remaining, err := io.ReadAll(reader.R)
	if err != nil || len(bytes.TrimSpace(remaining)) != 0 || singleField(header, "Feedback-Type") != "abuse" || singleField(header, "Version") != "1" || singleField(header, "User-Agent") == "" {
		return Report{}, ErrInvalid
	}
	to, err := feedbackMailbox(singleField(header, "Original-Rcpt-To"))
	if err != nil || to != correlation.RecipientAddress {
		return Report{}, ErrInvalid
	}
	from, err := feedbackMailbox(singleField(header, "Original-Mail-From"))
	if err != nil || from != "bounce+"+correlation.RecipientID+"@"+correlation.MessageDomain {
		return Report{}, ErrInvalid
	}
	correlation.Kind, correlation.Code = "complained", "arf_abuse"
	return correlation, nil
}

func feedbackMailbox(value string) (string, error) {
	if strings.HasPrefix(value, "<") && strings.HasSuffix(value, ">") {
		value = value[1 : len(value)-1]
	}
	address, err := parseAddress(value)
	if err != nil || address.Name != "" || address.Address != value {
		return "", ErrInvalid
	}
	return address.Address, nil
}
