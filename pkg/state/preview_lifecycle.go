package state

import "time"

// PRPreviewClosedGrace is how long a GitHub preview remains available after
// its pull request closes. The close webhook stamps this deadline atomically
// with the closed state; it is intentionally separate from the open-preview
// TTL configured by the project.
const PRPreviewClosedGrace = 24 * time.Hour
