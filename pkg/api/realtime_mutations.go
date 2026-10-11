package api

import (
	"context"
	"fmt"
	"net/url"
)

type ManagedRealtimeMessageMutationRequest struct {
	ExpectedVersion int64  `json:"expected_version"`
	DataBase64      string `json:"data_base64,omitempty"`
	Binary          bool   `json:"binary,omitempty"`
}

type ManagedRealtimeMessageMutationResponse struct {
	MessageID string `json:"message_id"`
	Version   int64  `json:"version"`
	Sequence  int64  `json:"sequence"`
	Event     string `json:"event"`
	Deleted   bool   `json:"deleted"`
}

func (c *Client) MutateManagedRealtimeMessage(ctx context.Context, slug, ep, stream string, inbox bool, id string, remove bool, request ManagedRealtimeMessageMutationRequest) (ManagedRealtimeMessageMutationResponse, error) {
	path := fmt.Sprintf("/v1/apps/%s/realtime/endpoints/%s/channels/%s/retained-messages/%s", url.PathEscape(slug), url.PathEscape(ep), url.PathEscape(stream), url.PathEscape(id))
	if inbox {
		path = fmt.Sprintf("/v1/apps/%s/realtime/endpoints/%s/principals/inbox/%s?principal=%s", url.PathEscape(slug), url.PathEscape(ep), url.PathEscape(id), url.QueryEscape(stream))
	}
	method := "PATCH"
	if remove {
		method = "DELETE"
	}
	var response ManagedRealtimeMessageMutationResponse
	err := c.do(ctx, method, path, request, &response)
	return response, err
}
