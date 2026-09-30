# Platform-owned shared read-only base for networkless python-data-v1 Runs.
# Package changes require a new profile ID, never an in-place v1 update.
FROM cgr.dev/chainguard/wolfi-base:latest@sha256:918a593b8268c222afd4e2c4f06860ac984e60719b4697e4c71d796bc8fcd042
RUN apk add --no-cache bash ca-certificates python-3.13 libstdc++ && \
    mkdir -p /usr/local/bin /etc/faas /usr/share/faas && \
    ln -sf /usr/bin/python3.13 /usr/local/bin/python3
COPY images/rootfs-skel/ /
COPY pkg/executionprofiles/python-data-v1.json /usr/share/faas/execution-profile.json
COPY images/install-execution-profile.py /tmp/install-execution-profile.py
RUN python3.13 -I -S /tmp/install-execution-profile.py /usr/share/faas/execution-profile.json && \
    printf '%s\n' '{"kind":"execution","version":1,"profile":"python-data-v1"}' > /etc/faas/execution.json && \
    rm -f /tmp/install-execution-profile.py /etc/shadow
WORKDIR /app
