# base-debian-parent — shared staging-only parent for the Debian-backed
# runtimes (ADR-053). Keep this Dockerfile as a direct FROM so its first
# layer is byte-identical to the Debian layer used by node24/python312/
# python313. imaged composes those children by matching OCI diff IDs.
#
# Node22 is intentionally Alpine and does not use this parent.
FROM mirror.gcr.io/library/debian:13-slim@sha256:4876fdea29cac1a33a3195f1eb1952feb1fca7dd6dd00be3e2f1879cf20d34d2
