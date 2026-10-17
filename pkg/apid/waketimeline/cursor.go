package waketimeline

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type pageCursor struct {
	Version int    `json:"v"`
	Account string `json:"account"`
	App     string `json:"app"`
	Wake    string `json:"wake"`
	Since   string `json:"since"`
	Limit   int    `json:"limit"`
	At      string `json:"at"`
	ID      string `json:"id"`
}

func cursorContext(account, app, wake string, p selection) pageCursor {
	return pageCursor{Version: 1, Account: account, App: app, Wake: wake, Since: p.since, Limit: p.limit}
}

func encodeCursor(c pageCursor, last state.Event) (string, error) {
	c.At, c.ID = last.At.UTC().Format(time.RFC3339Nano), strconv.FormatInt(last.ID, 10)
	data, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	raw := "wk1." + base64.RawURLEncoding.EncodeToString(data)
	if len(raw) > api.WakeTimelineCursorMaxBytes {
		return "", errors.New("wake cursor exceeds bound")
	}
	return raw, nil
}

func decodeCursor(raw string, want pageCursor) (state.WakeTimelinePosition, error) {
	fail := errors.New("invalid wake timeline cursor context")
	prefix, ok := strings.CutPrefix(raw, "wk1.")
	if !ok || len(raw) > api.WakeTimelineCursorMaxBytes {
		return state.WakeTimelinePosition{}, fail
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(prefix)
	if err != nil {
		return state.WakeTimelinePosition{}, fail
	}
	var c pageCursor
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Version != want.Version || c.Account != want.Account || c.App != want.App || c.Wake != want.Wake || c.Since != want.Since || c.Limit != want.Limit {
		return state.WakeTimelinePosition{}, fail
	}
	at, err := time.Parse(time.RFC3339Nano, c.At)
	id, ie := strconv.ParseInt(c.ID, 10, 64)
	if err != nil || ie != nil || at.IsZero() || at.UTC().Format(time.RFC3339Nano) != c.At || !at.Equal(at.Truncate(time.Microsecond)) || id < 1 || strconv.FormatInt(id, 10) != c.ID {
		return state.WakeTimelinePosition{}, fail
	}
	return state.WakeTimelinePosition{At: at, ID: id}, nil
}
