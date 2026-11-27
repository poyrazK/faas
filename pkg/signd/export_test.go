package signd

import (
	"context"
	"time"
)

// SetRetryWait replaces the Unavailable retry sleep so tests do not wait.
func (c *Client) SetRetryWait(wait func(context.Context, time.Duration) error) { c.wait = wait }
