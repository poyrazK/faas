package api

import (
	"context"
	"fmt"
	"net/url"
)

type ManagedRealtimeReadProgressResponse struct {
	Sequence           int64  `json:"sequence"`
	Unread             int64  `json:"unread"`
	OldestSequence     int64  `json:"oldest_sequence"`
	LatestSequence     int64  `json:"latest_sequence"`
	HistoryUnavailable bool   `json:"history_unavailable"`
	UpdatedAt          string `json:"updated_at,omitempty"`
}

type ManagedRealtimeReadProgressRequest struct {
	Sequence int64 `json:"sequence"`
}

func (c *Client) ManagedRealtimeReadProgress(ctx context.Context, slug, ep, stream, principal string, inbox bool, advance *int64) (ManagedRealtimeReadProgressResponse, error) {
	path := fmt.Sprintf("/v1/apps/%s/realtime/endpoints/%s/channels/%s/read-progress?principal=%s", url.PathEscape(slug), url.PathEscape(ep), url.PathEscape(stream), url.QueryEscape(principal))
	if inbox {
		path = fmt.Sprintf("/v1/apps/%s/realtime/endpoints/%s/principals/inbox/read-progress?principal=%s", url.PathEscape(slug), url.PathEscape(ep), url.QueryEscape(principal))
	}
	method := "GET"
	var request any
	if advance != nil {
		method = "POST"
		request = ManagedRealtimeReadProgressRequest{Sequence: *advance}
	}
	var response ManagedRealtimeReadProgressResponse
	err := c.do(ctx, method, path, request, &response)
	return response, err
}
