package api

import "time"

// MinInstancesFloorUnheldGrace is how long a min-instances floor keeps billing
// its synthetic gap after the app last had a billably resident instance. A
// floor is a promise that the platform holds warm capacity; short gaps
// (restart, redeploy rollover, a crash the customer's code caused and schedd
// replaces) stay billed, but a floor the platform has not held for longer
// than this (node outage, hostless boot failures, a platform park) is not
// capacity the customer received. A deployment younger than the grace still
// bills from t=0 (ADR-060). Account usage audit 2026-11-21: voting-redis was
// billed its full floor through a ~24 h platform outage.
const MinInstancesFloorUnheldGrace = 5 * time.Minute
