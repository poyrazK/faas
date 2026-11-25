package flagnotify

import (
	"fmt"
	"slices"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// CheckChannelIDs rejects ids that cannot be channel ids, before they reach
// the store.
func CheckChannelIDs(ids []string) error {
	if len(ids) > api.FlagsMaxNotificationChannels {
		return fmt.Errorf("at most %d notification channels, not %d", api.FlagsMaxNotificationChannels, len(ids))
	}
	for _, id := range ids {
		if _, err := uuid.Parse(id); err != nil {
			return fmt.Errorf("%q is not a notification channel id", id)
		}
	}
	return nil
}

// ValidChannels checks requested channel ids against the account's
// channels among them (found): at most FlagsMaxNotificationChannels, no
// duplicates, every one the account's. It returns the ids in order.
func ValidChannels(ids []string, found []state.NotificationChannel) ([]string, error) {
	if len(ids) > api.FlagsMaxNotificationChannels {
		return nil, fmt.Errorf("at most %d notification channels, not %d", api.FlagsMaxNotificationChannels, len(ids))
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if slices.Contains(out, id) {
			return nil, fmt.Errorf("channel %s is listed twice", id)
		}
		if !slices.ContainsFunc(found, func(c state.NotificationChannel) bool { return c.ID == id }) {
			return nil, fmt.Errorf("channel %s is not one of the account's notification channels (gregale alert channel list)", id)
		}
		out = append(out, id)
	}
	return out, nil
}
