package productemail

import "github.com/onebox-faas/faas/pkg/api"

func StatusLabel(status string) string {
	switch status {
	case "queued":
		return "Queued"
	case "leased":
		return "Sending (worker lease)"
	case "captured":
		return "Captured locally"
	case "provider_accepted":
		return "Accepted by email provider"
	case "delivered":
		return "Provider reported delivery"
	case "bounced":
		return "Provider reported a bounce"
	case "complained":
		return "Provider reported a complaint"
	case "failed":
		return "Failed"
	case "cancelled":
		return "Cancelled"
	default:
		return "Unavailable"
	}
}
func ReasonLabel(reason string) string {
	switch reason {
	case "":
		return ""
	case "access_changed":
		return "Workspace access or permissions changed."
	case "preferences_changed":
		return "Personal email choices or consent changed."
	case "unsubscribed":
		return "The recipient unsubscribed from notifications in this workspace."
	case "recipient_suppressed":
		return "Personal notifications are paused after a hard bounce or complaint."
	case "source_changed":
		return "A newer update replaced this message."
	case "authorization_unavailable":
		return "Workspace authorization is temporarily unavailable."
	case "transport_unavailable":
		return "The email transport is temporarily unavailable."
	case "provider_rejected":
		return "The provider rejected this message. Check the email provider configuration before retrying."
	case "retry_exhausted":
		return "The automatic attempt budget or retry window is exhausted."
	case "expired":
		return "The delivery window expired."
	default:
		return "Delivery details are unavailable."
	}
}
func RetryLabel(d api.ProductEmailOwnerDelivery) string {
	if d.RetryEligible {
		return "Retry available after correcting the provider issue. Current access will be checked again."
	}
	switch d.RetryBlockedReason {
	case "recipient_suppressed":
		return "This personal product/workspace subscription is paused after a hard bounce or complaint. Saving preferences or retrying cannot resume sending. Hard-bounce recovery requires the customer portal and a different verified address; complaints require review."
	case "transport_disabled":
		return "Email sending is disabled or unavailable."
	case "attempts_exhausted":
		return "No attempts remain."
	case "expired":
		return "The original delivery window expired."
	case "backoff_active":
		return "Waiting for the existing retry delay."
	case "preferences_changed":
		return "Personal email choices changed or consent expired."
	}
	switch d.Status {
	case "queued":
		return "The worker manages automatic retries and their delay."
	case "leased":
		return "A worker holds this message; refresh for its outcome."
	case "captured":
		return "Captured in the local sink. No email was sent."
	case "provider_accepted":
		return "The provider accepted this message. Inbox delivery has not been verified."
	case "delivered":
		return "The provider reported delivery to the receiving server. This does not prove inbox placement or that the message was read."
	case "bounced":
		return "The provider reported a bounce. This message cannot be resent. Permanent bounces pause personal notifications."
	case "complained":
		return "The provider reported a complaint. This message cannot be resent; personal notifications are paused."
	case "cancelled":
		return "Cancelled messages cannot be retried."
	default:
		return "This failure is not eligible for an owner retry."
	}
}
