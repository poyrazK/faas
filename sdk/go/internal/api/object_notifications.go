package api

// ObjectNotificationRule routes proven mutations to a Gregale-owned destination.
// Prefix and suffix are decoded key strings in the control API.
type ObjectNotificationRule struct {
	ID          string   `json:"id"`
	Destination string   `json:"destination"`
	Events      []string `json:"events"`
	Prefix      string   `json:"prefix,omitempty"`
	Suffix      string   `json:"suffix,omitempty"`
}

type ObjectBucketNotifications struct {
	BucketID string                   `json:"bucket_id"`
	Revision int64                    `json:"revision"`
	Rules    []ObjectNotificationRule `json:"rules"`
}

type ObjectBucketNotificationsRequest struct {
	Rules []ObjectNotificationRule `json:"rules"`
}
