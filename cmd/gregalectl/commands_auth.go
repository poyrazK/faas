package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/term"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	dispatchOperatorAuth = "auth"
	operatorSessionEnv   = "FAAS_OPERATOR_SESSION"
	operatorSessionFile  = "FAAS_OPERATOR_SESSION_FILE"
	operatorUserAgent    = "faas-cli/gregalectl"
)

type operatorSession struct {
	BaseURL     string    `json:"base_url"`
	Email       string    `json:"email"`
	AccountID   string    `json:"account_id"`
	Plan        string    `json:"plan"`
	Cookie      string    `json:"cookie"`
	ExpiresAt   time.Time `json:"expires_at"`
	SteppedUpAt time.Time `json:"stepped_up_at"`
	fromEnv     bool
}

type operatorHTTPClient struct {
	baseURL string
	http    *http.Client
	session *operatorSession
}

type operatorHTTPError struct {
	Status int
	Code   string
	Detail string
}

type operatorAuthStatus struct {
	BaseURL        string    `json:"base_url"`
	Email          string    `json:"email"`
	AccountID      string    `json:"account_id"`
	AccountStatus  string    `json:"account_status"`
	SessionExpires time.Time `json:"session_expires_at,omitempty"`
	StepUpExpires  time.Time `json:"step_up_expires_at,omitempty"`
	StepUpValid    bool      `json:"step_up_valid"`
}

func (e *operatorHTTPError) Error() string {
	if e.Code == api.CodeStepUpRequired {
		return "operator step-up required; run 'gregalectl auth step-up'"
	}
	if e.Code != "" && e.Detail != "" {
		return fmt.Sprintf("apid returned %d (%s): %s", e.Status, e.Code, e.Detail)
	}
	if e.Code != "" {
		return fmt.Sprintf("apid returned %d (%s)", e.Status, e.Code)
	}
	return fmt.Sprintf("apid returned %d", e.Status)
}

func cmdOperatorAuthDispatch(args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl auth: missing subcommand; want login|step-up|status|logout")
		return 2
	}
	switch args[0] {
	case "login":
		return cmdOperatorAuthLogin(args[1:])
	case "step-up":
		return cmdOperatorAuthStepUp(args[1:])
	case "status":
		return cmdOperatorAuthStatus(args[1:])
	case "logout":
		return cmdOperatorAuthLogout(args[1:])
	default:
		_, _ = fmt.Fprintf(osStderr, "gregalectl auth: unknown subcommand %q\n", args[0])
		return 2
	}
}

func cmdOperatorAuthLogin(args []string) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	email := fs.String("email", "", "FAAS_ADMIN_EMAILS-allowlisted operator email")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*email) == "" {
		_, _ = fmt.Fprintln(osStderr, "gregalectl auth login: --email required")
		return 2
	}
	if err := validateOperatorBaseURL(apidBase()); err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl auth login:", err)
		return 2
	}
	password, err := readOperatorSecret("Password: ")
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl auth login:", err)
		return 1
	}
	sess := operatorSession{BaseURL: apidBase(), Email: strings.ToLower(strings.TrimSpace(*email))}
	client := newOperatorHTTPClient(&sess)
	var login api.PasswordLoginResponse
	if err := client.doJSON(context.Background(), http.MethodPost, "/login", api.PasswordLoginRequest{Email: sess.Email, Password: password}, &login, false, nil); err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl auth login:", err)
		return 1
	}
	if sess.Cookie == "" {
		_, _ = fmt.Fprintln(osStderr, "gregalectl auth login: apid did not issue a session cookie")
		return 1
	}
	totp, err := readOperatorSecret("TOTP code: ")
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl auth login:", err)
		return 1
	}
	if err := client.verifyMFA(context.Background(), totp); err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl auth login:", err)
		return 1
	}
	sess.AccountID = login.AccountID
	sess.Plan = login.Plan
	if sess.ExpiresAt.IsZero() {
		sess.ExpiresAt = time.Now().UTC().Add(7 * 24 * time.Hour)
	}
	if err := saveOperatorSession(sess); err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl auth login: save session:", err)
		return 1
	}
	_, _ = fmt.Fprintf(osStdout, "operator session ready for %s; step-up valid for 5 minutes\n", sess.Email)
	return 0
}

func cmdOperatorAuthStepUp(args []string) int {
	fs := flag.NewFlagSet("step-up", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	sess, err := loadOperatorSession()
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl auth step-up:", err)
		return 1
	}
	if sess.fromEnv {
		_, _ = fmt.Fprintln(osStderr, "gregalectl auth step-up: cannot refresh a session supplied by FAAS_OPERATOR_SESSION; use 'auth login' with a session file")
		return 1
	}
	totp, err := readOperatorSecret("TOTP code: ")
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl auth step-up:", err)
		return 1
	}
	if err := newOperatorHTTPClient(&sess).verifyMFA(context.Background(), totp); err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl auth step-up:", err)
		return 1
	}
	if err := saveOperatorSession(sess); err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl auth step-up: save session:", err)
		return 1
	}
	_, _ = fmt.Fprintln(osStdout, "operator step-up valid for 5 minutes")
	return 0
}

func cmdOperatorAuthStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	sess, err := loadOperatorSession()
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl auth status:", err)
		return 1
	}
	var account api.AccountResponse
	if err := newOperatorHTTPClient(&sess).doJSON(context.Background(), http.MethodGet, "/v1/account", nil, &account, false, nil); err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl auth status:", err)
		return 1
	}
	stepUpUntil := sess.SteppedUpAt.Add(5 * time.Minute)
	status := operatorAuthStatus{
		BaseURL:        sess.BaseURL,
		Email:          account.Email,
		AccountID:      account.ID,
		AccountStatus:  account.Status,
		SessionExpires: sess.ExpiresAt,
		StepUpExpires:  stepUpUntil,
		StepUpValid:    time.Now().Before(stepUpUntil),
	}
	if jsonEnabled() {
		return emitOperatorJSON(status)
	}
	stepUp := "expired"
	if status.StepUpValid {
		stepUp = "valid until " + stepUpUntil.Format(time.RFC3339)
	}
	_, _ = fmt.Fprintf(osStdout, "email=%s\naccount_id=%s\nbase_url=%s\nstep_up=%s\n", account.Email, account.ID, sess.BaseURL, stepUp)
	return 0
}

func cmdOperatorAuthLogout(args []string) int {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	sess, err := loadOperatorSession()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			_, _ = fmt.Fprintln(osStdout, "no operator session")
			return 0
		}
		_, _ = fmt.Fprintln(osStderr, "gregalectl auth logout:", err)
		return 1
	}
	client := newOperatorHTTPClient(&sess)
	var csrf api.CSRFTokenResponse
	if err := client.doJSON(context.Background(), http.MethodGet, "/v1/auth/csrf?action=auth.logout", nil, &csrf, false, nil); err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl auth logout:", err)
		return 1
	}
	if err := client.doJSON(context.Background(), http.MethodPost, "/v1/auth/logout", map[string]string{"csrf_token": csrf.CSRFToken}, nil, false, []*http.Cookie{{Name: "faas_csrf", Value: csrf.CSRFToken}}); err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl auth logout:", err)
		return 1
	}
	if err := deleteOperatorSession(); err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl auth logout: remove local session:", err)
		return 1
	}
	_, _ = fmt.Fprintln(osStdout, "operator session revoked")
	return 0
}

func newOperatorHTTPClient(sess *operatorSession) *operatorHTTPClient {
	return &operatorHTTPClient{
		baseURL: strings.TrimRight(sess.BaseURL, "/"),
		http: &http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		session: sess,
	}
}

func (c *operatorHTTPClient) verifyMFA(ctx context.Context, totp string) error {
	if strings.TrimSpace(totp) == "" {
		return errors.New("TOTP code is required")
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v1/account/mfa/verify", api.MFAVerifyRequest{Totp: strings.TrimSpace(totp)}, nil, true, nil); err != nil {
		return err
	}
	c.session.SteppedUpAt = time.Now().UTC()
	return nil
}

func (c *operatorHTTPClient) doJSON(ctx context.Context, method, path string, input, output any, idempotent bool, extraCookies []*http.Cookie) error {
	return c.doJSONWithHeaders(ctx, method, path, input, output, idempotent, extraCookies, nil)
}

func (c *operatorHTTPClient) doJSONWithHeaders(ctx context.Context, method, path string, input, output any, idempotent bool, extraCookies []*http.Cookie, extraHeaders http.Header) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", operatorUserAgent)
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotent {
		req.Header.Set("Idempotency-Key", uuid.NewString())
	}
	for name, values := range extraHeaders {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	if c.session != nil && c.session.Cookie != "" {
		req.AddCookie(&http.Cookie{Name: "faas_sid", Value: c.session.Cookie})
	}
	for _, cookie := range extraCookies {
		req.AddCookie(cookie)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("dial apid: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	for _, cookie := range resp.Cookies() {
		if cookie.Name != "faas_sid" {
			continue
		}
		c.session.Cookie = cookie.Value
		if cookie.MaxAge > 0 {
			c.session.ExpiresAt = time.Now().UTC().Add(time.Duration(cookie.MaxAge) * time.Second)
		} else if !cookie.Expires.IsZero() {
			c.session.ExpiresAt = cookie.Expires.UTC()
		}
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var problem api.Problem
		_ = json.Unmarshal(payload, &problem)
		return &operatorHTTPError{Status: resp.StatusCode, Code: problem.Code, Detail: problem.Detail}
	}
	if output != nil && len(bytes.TrimSpace(payload)) > 0 {
		if err := json.Unmarshal(payload, output); err != nil {
			return fmt.Errorf("decode apid response: %w", err)
		}
	}
	return nil
}

func operatorSessionPath() (string, error) {
	if value := strings.TrimSpace(os.Getenv(operatorSessionFile)); value != "" {
		return value, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "gregale", "operator-session.json"), nil
}

func loadOperatorSession() (operatorSession, error) {
	if cookie := strings.TrimSpace(os.Getenv(operatorSessionEnv)); cookie != "" {
		baseURL := apidBase()
		if err := validateOperatorBaseURL(baseURL); err != nil {
			return operatorSession{}, err
		}
		return operatorSession{BaseURL: baseURL, Cookie: cookie, fromEnv: true}, nil
	}
	path, err := operatorSessionPath()
	if err != nil {
		return operatorSession{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return operatorSession{}, fmt.Errorf("%w: no operator session; run 'gregalectl auth login --email <email>'", os.ErrNotExist)
		}
		return operatorSession{}, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return operatorSession{}, fmt.Errorf("refusing session file %s with permissions %04o; want 0600", path, info.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return operatorSession{}, err
	}
	var sess operatorSession
	if err := json.Unmarshal(raw, &sess); err != nil {
		return operatorSession{}, fmt.Errorf("decode %s: %w", path, err)
	}
	if sess.Cookie == "" || sess.BaseURL == "" {
		return operatorSession{}, fmt.Errorf("session file %s is incomplete", path)
	}
	if err := validateOperatorBaseURL(sess.BaseURL); err != nil {
		return operatorSession{}, err
	}
	if strings.TrimRight(sess.BaseURL, "/") != apidBase() {
		return operatorSession{}, fmt.Errorf("session belongs to %s, but FAAS_APID_URL resolves to %s; log in again", sess.BaseURL, apidBase())
	}
	if !sess.ExpiresAt.IsZero() && time.Now().After(sess.ExpiresAt) {
		return operatorSession{}, errors.New("operator session expired; run 'gregalectl auth login --email <email>'")
	}
	return sess, nil
}

func validateOperatorBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("invalid FAAS_APID_URL %q", raw)
	}
	if u.Scheme == "https" {
		return nil
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme == "http" && (strings.EqualFold(u.Hostname(), "localhost") || (ip != nil && ip.IsLoopback())) {
		return nil
	}
	return fmt.Errorf("refusing to send operator credentials to non-HTTPS FAAS_APID_URL %q", raw)
}

func saveOperatorSession(sess operatorSession) error {
	if sess.fromEnv {
		return errors.New("cannot replace a session supplied by FAAS_OPERATOR_SESSION")
	}
	path, err := operatorSessionPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".operator-session-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func deleteOperatorSession() error {
	path, err := operatorSessionPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func readOperatorSecret(prompt string) (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("interactive terminal required")
	}
	_, _ = fmt.Fprint(osStderr, prompt)
	raw, err := term.ReadPassword(int(os.Stdin.Fd()))
	_, _ = fmt.Fprintln(osStderr)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func emitOperatorJSON(value any) int {
	enc := json.NewEncoder(osStdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		_, _ = fmt.Fprintln(osStderr, err)
		return 1
	}
	return 0
}

func nodeMutationPath(node, action, reason string) string {
	return "/v1/admin/ops/nodes/" + url.PathEscape(node) + "/" + action + "?confirm=true&reason=" + url.QueryEscape(reason)
}
