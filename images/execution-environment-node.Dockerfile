# Operator-published installer base. Both arguments must be digest-pinned.
# NODE_TOOLS_REF must have the same Node major and libc as RUNTIME_BASE_REF.
ARG NODE_TOOLS_REF
ARG RUNTIME_BASE_REF
FROM ${NODE_TOOLS_REF} AS package_manager
FROM ${RUNTIME_BASE_REF}
USER root
COPY --from=package_manager /usr/local/lib/node_modules/npm /usr/local/lib/node_modules/npm
RUN ln -sf /usr/local/lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm && \
    mkdir -p /etc/faas /opt/gregale/environment
WORKDIR /opt/gregale/environment
