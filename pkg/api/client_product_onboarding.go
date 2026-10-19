package api

import (
	"context"
	"net/http"
	"net/url"
)

func (c *Client) ListProductOnboardingWorkspaces(ctx context.Context, slug, authToken string) (ProductOnboardingWorkspaces, error) {
	var out ProductOnboardingWorkspaces
	err := c.do(ctx, http.MethodPost, productPath(slug)+"/onboarding/workspaces", ProductOnboardingAuthRequest{AuthAccessToken: authToken}, &out)
	return out, err
}

func (c *Client) PreviewProductOnboarding(ctx context.Context, slug string, req ProductOnboardingRequest, authToken string) (ProductOnboardingPreview, error) {
	var out ProductOnboardingPreview
	err := c.do(ctx, http.MethodPost, productPath(slug)+"/onboarding/preview", ProductOnboardingReviewRequest{Request: req, AuthAccessToken: authToken}, &out)
	return out, err
}

func (c *Client) ConfirmProductOnboarding(ctx context.Context, slug string, confirmation ProductOnboardingConfirmation, authToken string) (ProductOnboardingReceipt, error) {
	var out ProductOnboardingReceipt
	err := c.do(ctx, http.MethodPost, productPath(slug)+"/onboarding/confirm", ProductOnboardingApplyRequest{Confirmation: confirmation, AuthAccessToken: authToken}, &out)
	return out, err
}

func (c *Client) GetProductOnboarding(ctx context.Context, slug, id string) (ProductOnboardingStatus, error) {
	var out ProductOnboardingStatus
	err := c.do(ctx, http.MethodGet, productPath(slug)+"/onboarding/"+url.PathEscape(id), nil, &out)
	return out, err
}
