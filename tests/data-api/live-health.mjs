// /healthz caches a completed set of checks for limits.healthCacheMs so public
// polling cannot keep the database busy. Real-engine suites assert health
// transitions (saturation, recovery, an engine restart) a few milliseconds
// apart, so they drive the cache with a clock that moves past it on every
// read. The cache itself is covered by the runtime's unit tests.
export function liveHealth(config) {
  let at = 0
  config.healthClock = () => (at += 3_600_000)
  return config
}
