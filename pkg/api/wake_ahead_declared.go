package api

// ServiceWakeAheadDeclaredMaxMisses stops wake-ahead from speculatively
// waking a declared (depends_on) target after this many consecutive wakes
// the caller did not use, until a call to it is observed again. Each unused
// speculative wake is billed to the target's idle timeout, and a caller that
// never collects ServiceWakeAheadMinWakes measured wakes otherwise kept
// waking every declared target forever (account usage audit 2026-11-21).
const ServiceWakeAheadDeclaredMaxMisses = 3
