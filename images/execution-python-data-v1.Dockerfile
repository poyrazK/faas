# Platform-owned shared read-only base for networkless python-data-v1 Runs.
# Package changes require a new profile ID, never an in-place v1 update.
FROM cgr.dev/chainguard/wolfi-base:latest@sha256:73f2cab0f11208427595d4bdd784046bc4116ef7cc52f0c1b48b6b7f820288d5
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
