# base-debian-parent — shared staging-only parent for the Debian-backed
# runtimes (ADR-053). Keep this Dockerfile as a direct FROM so its first
# layer is byte-identical to the Debian layer used by node24/python312/
# python313. imaged composes those children by matching OCI diff IDs.
#
# Node22 is intentionally Alpine and does not use this parent.
FROM public.ecr.aws/docker/library/debian:12-slim@sha256:a4672c0cb26fbdde88e38fa2dfb6c681942306680e41e4378b28770b6e79ee91
