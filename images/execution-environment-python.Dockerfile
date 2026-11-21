# Operator-published Wolfi installer base; pin the runtime reference by digest.
ARG RUNTIME_BASE_REF
FROM ${RUNTIME_BASE_REF}
ARG PIP_PACKAGE=py3.13-pip
USER root
RUN apk add --no-cache ${PIP_PACKAGE} && \
    mkdir -p /etc/faas /opt/gregale/environment
WORKDIR /opt/gregale/environment
