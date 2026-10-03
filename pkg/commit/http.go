package commit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPAcceptor uses an operator-configured Gregale endpoint. Credentials and
// response bodies are deliberately excluded from returned error messages.
type HTTPAcceptor struct {
	URL    string
	Token  string
	Client *http.Client
}

func (a *HTTPAcceptor) Accept(ctx context.Context, e Event) (Receipt, error) {
	data, err := json.Marshal(e)
	if err != nil {
		return Receipt{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.URL, bytes.NewReader(data))
	if err != nil {
		return Receipt{}, errors.New("commit: invalid acceptance endpoint")
	}
	req.Header.Set("Authorization", "Bearer "+a.Token)
	req.Header.Set("Content-Type", "application/json")
	// Clone an injected client rather than mutating shared transport settings.
	// Every client must refuse redirects: an operator endpoint cannot delegate
	// the event payload and bearer credential to another location.
	client := http.Client{Timeout: 15 * time.Second}
	if a.Client != nil {
		client = *a.Client
		if client.Timeout == 0 {
			client.Timeout = 15 * time.Second
		}
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		return Receipt{}, errors.New("commit: acceptance endpoint unavailable")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusAccepted {
		switch response.StatusCode {
		case http.StatusBadRequest:
			return Receipt{}, &PermanentError{Code: "acceptance_rejected"}
		case http.StatusConflict:
			var problem struct {
				Code string `json:"code"`
			}
			_ = json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&problem)
			if problem.Code == "commit_identity_conflict" {
				return Receipt{}, &PermanentError{Code: "identity_conflict"}
			}
			return Receipt{}, errors.New("commit: acceptance temporarily unavailable")
		default:
			return Receipt{}, errors.New("commit: acceptance temporarily unavailable")
		}
	}
	var receipt Receipt
	decoder := json.NewDecoder(io.LimitReader(response.Body, 8192))
	if err := decoder.Decode(&receipt); err != nil {
		return Receipt{}, errors.New("commit: invalid acceptance response")
	}
	if strings.TrimSpace(receipt.ID) == "" {
		return Receipt{}, errors.New("commit: acceptance receipt missing")
	}
	return receipt, nil
}
