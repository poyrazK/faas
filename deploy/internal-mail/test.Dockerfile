# Disposable local-only qualification fixture; never a production mail image.
FROM debian:trixie-slim@sha256:a29215f6a35e51e22adffa17f89e9d2ef06214e64a2bad10d765c46aea49f11f
RUN --mount=type=secret,id=proxy_ca \
    set -eu; \
    printf '#!/bin/sh\nexit 101\n' > /usr/sbin/policy-rc.d; \
    chmod 0755 /usr/sbin/policy-rc.d; \
    set --; \
    if [ -s /run/secrets/proxy_ca ]; then \
        set -- -o Acquire::https::CaInfo=/run/secrets/proxy_ca; \
    fi; \
    apt-get "$@" update; \
    DEBIAN_FRONTEND=noninteractive apt-get "$@" install -y --no-install-recommends postfix ca-certificates; \
    rm -rf /var/lib/apt/lists/*; \
    groupadd --system faas; \
    useradd --system --gid faas --no-create-home faas; \
    touch /run/gregale-postfix-test
COPY --chmod=0755 mail-report /opt/faas/current/bin/mail-report
COPY faas-mail-report /usr/local/libexec/faas-mail-report
COPY --chmod=0755 postfix.test /postfix.test
COPY postfix-*.example /fixtures/
RUN chmod 0755 /usr/local/libexec/faas-mail-report
ENV FAAS_TEST_POSTFIX=1
CMD ["/postfix.test", "-test.run=^TestPostfixRelayBridge$", "-test.v", "-test.timeout=2m"]
