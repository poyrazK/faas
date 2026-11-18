# runner-python312 — base rootfs (drive0) for Python 3.12 apps and functions
# (spec §4.6, §4.9). Same two-drive rationale as runner-node22.
# Content-addressed, staged to /srv/fc/base/runner-python312.ext4.
# Debug tools for `gregale app exec --debug` (ADR-20261117070620): the pinned
# static BusyBox (root:root 0555) with applet links, at /usr/lib/gregale/debug.
# guest-init mounts it only into a debug session's private mount namespace; it
# is not on any PATH. Every runtime base carries the same BusyBox.
FROM mirror.gcr.io/library/busybox:1.37.0-musl@sha256:f6a95d611e0a8780fabdb2aec43e31275b2720dfefa3e41b16835bfc42454478 AS gregale-debug-tools
RUN set -eu; mkdir -p /out/bin; cp /bin/busybox /out/busybox; \
    for applet in $(/bin/busybox --list); do ln -s ../busybox "/out/bin/$applet"; done; \
    chmod 0555 /out/busybox

FROM mirror.gcr.io/library/python:3.12-slim-trixie@sha256:05a994d2344eb364cc186910f4fc8786a38215f39f00d0acc25bff33a08f4177
# Issue #197 B3.6: mutable tag pinned via images/Dockerfile.lock.
RUN --mount=type=secret,id=proxy_ca,target=/etc/ssl/certs/ca-certificates.crt apt-get update && DEBIAN_FRONTEND=noninteractive \
    apt-get upgrade -y --no-install-recommends && \
    rm -rf /var/lib/apt/lists/* && \
    (id app 2>/dev/null || useradd -u 1000 -m app)
COPY --chmod=0644 images/runner-python312-pip.txt /tmp/pip-requirements.txt
RUN --mount=type=secret,id=proxy_ca,target=/etc/ssl/certs/ca-certificates.crt \
    PIP_CERT=/etc/ssl/certs/ca-certificates.crt pip install --no-cache-dir --require-hashes --only-binary=:all: -r /tmp/pip-requirements.txt && \
    rm /tmp/pip-requirements.txt
COPY --chmod=0644 guest/profiling/python/requirements.txt /opt/gregale/profiling/requirements.txt
RUN --mount=type=secret,id=proxy_ca,target=/etc/ssl/certs/ca-certificates.crt mkdir -p /opt/gregale/profiling/python && \
    chmod 0755 /opt/gregale /opt/gregale/profiling /opt/gregale/profiling/python && \
    PIP_CERT=/etc/ssl/certs/ca-certificates.crt pip install --no-cache-dir --require-hashes --only-binary=:all: -r /opt/gregale/profiling/requirements.txt
COPY --chmod=0644 guest/profiling/python/sitecustomize.py /opt/gregale/profiling/python/sitecustomize.py
COPY --chmod=0644 guest/profiling/python/_gregale_heap.py /opt/gregale/profiling/python/_gregale_heap.py
COPY --chmod=0644 guest/profiling/python/_gregale_stacks.py /opt/gregale/profiling/python/_gregale_stacks.py
COPY --chmod=0644 guest/tracing/python/requirements.txt /opt/gregale/tracing/requirements.txt
RUN --mount=type=secret,id=proxy_ca,target=/etc/ssl/certs/ca-certificates.crt mkdir -p /opt/gregale/tracing/python/lib && \
    chmod 0755 /opt/gregale/tracing /opt/gregale/tracing/python /opt/gregale/tracing/python/lib && \
    PIP_CERT=/etc/ssl/certs/ca-certificates.crt pip install --no-cache-dir --require-hashes --only-binary=:all: \
      --target /opt/gregale/tracing/python/lib -r /opt/gregale/tracing/requirements.txt && \
    mkdir -p /opt/gregale/tracing/python/deps && cd /opt/gregale/tracing/python/lib && \
    for f in *; do case "$f" in opentelemetry*) ;; *) mv "$f" ../deps/ ;; esac; done && \
    chmod 0755 /opt/gregale/tracing/python/deps
COPY --chmod=0644 guest/tracing/python/sitecustomize.py /opt/gregale/tracing/python/sitecustomize.py
WORKDIR /app

# ADR-20261117070620: debug tools for `gregale app exec --debug`.
COPY --from=gregale-debug-tools /out/ /usr/lib/gregale/debug/
