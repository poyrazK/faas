# Operator-published Wolfi installer base, pinned by digest: the FROM takes the
# image and its digest (sha256:...) as separate arguments, so a build without
# a digest fails.
ARG RUNTIME_BASE_IMAGE
ARG RUNTIME_BASE_DIGEST
FROM ${RUNTIME_BASE_IMAGE}@${RUNTIME_BASE_DIGEST}
ARG PIP_PACKAGE=py3.13-pip
USER root
RUN apk add --no-cache ${PIP_PACKAGE} && \
    mkdir -p /etc/faas /opt/gregale/environment
WORKDIR /opt/gregale/environment
