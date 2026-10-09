# runner-python312 — base rootfs (drive0) for Python 3.12 apps and functions
# (spec §4.6, §4.9). Same two-drive rationale as runner-node22.
# Content-addressed, staged to /srv/fc/base/runner-python312.ext4.
FROM python:3.12-slim-bookworm@sha256:2ed6491b93cd49272ee6de2b5a38440c3448360322c089fc23e370722d74179d
# Issue #197 B3.6: mutable tag pinned via images/Dockerfile.lock.
RUN --mount=type=secret,id=proxy_ca,target=/etc/ssl/certs/ca-certificates.crt apt-get update && DEBIAN_FRONTEND=noninteractive \
    apt-get upgrade -y --no-install-recommends && \
    rm -rf /var/lib/apt/lists/* && \
    (id app 2>/dev/null || useradd -u 1000 -m app)
COPY --chmod=0644 guest/profiling/python/requirements.txt /opt/gregale/profiling/requirements.txt
RUN --mount=type=secret,id=proxy_ca,target=/etc/ssl/certs/ca-certificates.crt mkdir -p /opt/gregale/profiling/python && \
    chmod 0755 /opt/gregale /opt/gregale/profiling /opt/gregale/profiling/python && \
    PIP_CERT=/etc/ssl/certs/ca-certificates.crt pip install --no-cache-dir --require-hashes --only-binary=:all: -r /opt/gregale/profiling/requirements.txt
COPY --chmod=0644 guest/profiling/python/sitecustomize.py /opt/gregale/profiling/python/sitecustomize.py
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
