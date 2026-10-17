package internalmail

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/netip"
	"net/textproto"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/emersion/go-msgauth/dkim"
	"github.com/onebox-faas/faas/pkg/api"
)

func validDomain(name string) bool {
	if len(name) > 253 || len(name) < 3 || !strings.Contains(name, ".") {
		return false
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !lowerAlphaNumeric(r) && r != '-' {
				return false
			}
		}
	}
	return true
}

func parseAddress(value string) (*mail.Address, error) {
	if strings.ContainsAny(value, "\r\n\x00") {
		return nil, ErrInvalid
	}
	a, err := mail.ParseAddress(value)
	if err != nil {
		return nil, ErrInvalid
	}
	local, domain, ok := strings.Cut(a.Address, "@")
	if !ok || local == "" || len(a.Address) > 254 || !validDomain(strings.ToLower(domain)) {
		return nil, ErrInvalid
	}
	for _, c := range a.Address {
		if c < 33 || c > 126 {
			return nil, ErrInvalid
		}
	}
	a.Address = local + "@" + strings.ToLower(domain)
	return a, nil
}

func normalizeRequest(r SendRequest) (SendRequest, string, error) {
	timing, err := r.normalized()
	if err != nil {
		return r, "", err
	}
	r.MessageTiming = timing
	a, err := parseAddress(r.From)
	if err != nil {
		return r, "", err
	}
	r.From = a.String()
	if len(r.To) == 0 || len(r.To) > api.InternalMailMaxRecipients || len(r.Subject) == 0 || len(r.Subject) > api.InternalMailMaxSubjectBytes || strings.ContainsAny(r.Subject, "\r\n\x00") {
		return r, "", ErrInvalid
	}
	if r.Text == "" || len(r.Text)+len(r.HTML) > api.InternalMailMaxBodyBytes || !utf8.ValidString(r.Text) || !utf8.ValidString(r.HTML) || !utf8.ValidString(r.Subject) || len(r.Headers) > api.InternalMailMaxHeaders {
		return r, "", ErrInvalid
	}
	seen := map[string]bool{}
	to := make([]string, 0, len(r.To))
	for _, v := range r.To {
		recipient, err := parseAddress(v)
		if err != nil {
			return r, "", err
		}
		if !seen[recipient.Address] {
			seen[recipient.Address] = true
			to = append(to, recipient.Address)
		}
	}
	sort.Strings(to)
	r.To = to
	headers, err := normalizeHeaders(r.Headers)
	if err != nil {
		return r, "", err
	}
	r.Headers = headers
	_, domain, _ := strings.Cut(a.Address, "@")
	return r, domain, nil
}

func normalizeHeaders(headers map[string]string) (map[string]string, error) {
	if len(headers) == 0 {
		return nil, nil
	}
	result := make(map[string]string, len(headers))
	protected := map[string]bool{"From": true, "To": true, "Cc": true, "Bcc": true, "Sender": true, "Return-Path": true, "Received": true, "Dkim-Signature": true, "Message-Id": true, "Date": true, "Subject": true, "Mime-Version": true, "Content-Type": true, "Content-Transfer-Encoding": true, "X-Gregale-Mail-Recipient-Id": true}
	for key, value := range headers {
		if key == "" || len(key)+len(value) > api.InternalMailMaxHeaderBytes || strings.ContainsAny(value, "\r\n\x00") || !utf8.ValidString(value) {
			return nil, ErrInvalid
		}
		for _, r := range key {
			if !lowerAlphaNumeric(r) && (r < 'A' || r > 'Z') && r != '-' {
				return nil, ErrInvalid
			}
		}
		key = textproto.CanonicalMIMEHeaderKey(key)
		if protected[key] {
			return nil, ErrInvalid
		}
		if _, exists := result[key]; exists {
			return nil, ErrInvalid
		}
		result[key] = value
	}
	return result, nil
}

func (s *Service) render(message Message, recipient Recipient, domain Domain) ([]byte, error) {
	plain, err := s.Codec.Open("message:"+message.ID, message.SealedPayload)
	if err != nil {
		return nil, err
	}
	defer clear(plain)
	var request SendRequest
	if err := json.Unmarshal(plain, &request); err != nil {
		return nil, err
	}
	request, fromDomain, err := normalizeRequest(request)
	if err != nil || fromDomain != domain.Name || !sameTiming(request.MessageTiming, message.MessageTiming) {
		return nil, ErrInvalid
	}
	resource := "domain:" + domain.ID
	if domain.DKIMKeyID != "" {
		resource = dkimKeyResource(domain.ID, domain.DKIMKeyID)
	}
	sealedKey, err := s.Codec.Open(resource, domain.SealedKey)
	if err != nil {
		return nil, err
	}
	defer clear(sealedKey)
	block, _ := pem.Decode(sealedKey)
	if block == nil {
		return nil, ErrInvalid
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	data, err := renderMessage(request, recipient, domain.Name, message.CreatedAt)
	if err != nil {
		return nil, err
	}
	var signed bytes.Buffer
	err = dkim.Sign(&signed, bytes.NewReader(data), &dkim.SignOptions{Domain: domain.Name, Selector: domain.Selector, Signer: key, HeaderCanonicalization: dkim.CanonicalizationRelaxed, BodyCanonicalization: dkim.CanonicalizationRelaxed})
	if err != nil {
		return nil, err
	}
	return signed.Bytes(), nil
}

func renderMessage(r SendRequest, recipient Recipient, domain string, created time.Time) ([]byte, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.SetBoundary("gregale-" + recipient.ID); err != nil {
		return nil, err
	}
	parts := []struct{ kind, value string }{{"text/plain", r.Text}}
	if r.HTML != "" {
		parts = append(parts, struct{ kind, value string }{"text/html", r.HTML})
	}
	for _, part := range parts {
		p, err := writer.CreatePart(textproto.MIMEHeader{"Content-Type": {part.kind + "; charset=utf-8"}, "Content-Transfer-Encoding": {"quoted-printable"}})
		if err != nil {
			return nil, err
		}
		qp := quotedprintable.NewWriter(p)
		if _, err := qp.Write([]byte(part.value)); err != nil {
			return nil, err
		}
		if err := qp.Close(); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	fmt.Fprintf(&out, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%s@%s>\r\nX-Gregale-Mail-Recipient-ID: %s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=%q\r\n", r.From, recipient.Address, mime.QEncoding.Encode("utf-8", r.Subject), created.Format(time.RFC1123Z), recipient.ID, domain, recipient.ID, writer.Boundary())
	keys := make([]string, 0, len(r.Headers))
	for key := range r.Headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(&out, "%s: %s\r\n", key, r.Headers[key])
	}
	out.WriteString("\r\n")
	out.Write(body.Bytes())
	return out.Bytes(), nil
}
