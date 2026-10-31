# base-debian-parent — shared staging-only parent for the Debian-backed
# runtimes (ADR-053). Keep this Dockerfile as a direct FROM so its first
# layer is byte-identical to the Debian layer used by node24/python312/
# python313. imaged composes those children by matching OCI diff IDs.
#
# Node22 is intentionally Alpine and does not use this parent.
FROM mirror.gcr.io/library/debian:13-slim@sha256:1e1a22175fbf1b32157ddaf0219e1db4e5401fab2b94ed285add64d18587cd2d
