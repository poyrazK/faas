# base-debian-parent — shared staging-only parent for the Debian-backed
# runtimes (ADR-053). Keep this Dockerfile as a direct FROM so its first
# layer is byte-identical to the Debian 13 layer used by node24 and
# python312. imaged composes those children by matching OCI diff IDs.
# Debian 13 includes the patched Perl package; keep both child pins on the
# same exact parent diff ID when changing this base.
#
# Node22 is intentionally Alpine and does not use this parent.
FROM debian:13-slim@sha256:918311b7b6c4c6f68b232ba516584925f6c78ad82b6fd534b98979df6438e483
