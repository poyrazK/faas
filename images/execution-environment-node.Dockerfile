# Operator-published installer bases, pinned by digest: each FROM takes the
# image and its digest (sha256:...) as separate arguments, so a build without
# a digest fails. NODE_TOOLS_IMAGE must have the same Node major and libc as
# RUNTIME_BASE_IMAGE.
ARG NODE_TOOLS_IMAGE
ARG NODE_TOOLS_DIGEST
ARG RUNTIME_BASE_IMAGE
ARG RUNTIME_BASE_DIGEST
FROM ${NODE_TOOLS_IMAGE}@${NODE_TOOLS_DIGEST} AS package_manager
FROM ${RUNTIME_BASE_IMAGE}@${RUNTIME_BASE_DIGEST}
USER root
COPY --from=package_manager /usr/local/lib/node_modules/npm /usr/local/lib/node_modules/npm
RUN ln -sf /usr/local/lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm && \
    mkdir -p /etc/faas /opt/gregale/environment
WORKDIR /opt/gregale/environment
