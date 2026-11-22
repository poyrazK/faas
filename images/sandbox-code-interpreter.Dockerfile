# The code interpreter's sandbox image (ADR-20261027090013 step 14).
#
# Sessions created with preset code-interpreter boot this image as an image
# session (apid FAAS_SANDBOX_CODE_INTERPRETER_IMAGE names its published
# digest). guest-init stays PID 1 and starts the driver below on the first
# code call, as the session's user; each code context is its own ipykernel,
# reached over IPC sockets in /run/gregale-code, never a TCP port.
#
# The Python packages are pinned with hashes in requirements.txt, compiled
# from requirements.in with pip-compile --generate-hashes.
FROM cgr.dev/chainguard/wolfi-base:latest@sha256:6b818b54724b30d095379a7471d9a9b6428a51ff52be188676d1d6e5ea67a1e4
COPY --chmod=0644 images/sandbox-code-interpreter/requirements.txt /tmp/requirements.txt
RUN apk add --no-cache bash ca-certificates git python-3.13 py3.13-pip libstdc++ && \
    ln -sf /usr/bin/python3.13 /usr/local/bin/python3 && \
    python3.13 -m pip install --no-cache-dir --require-hashes --only-binary=:all: --break-system-packages -r /tmp/requirements.txt && \
    python3.13 -m ipykernel install --name python3 --prefix /usr/local && \
    python3.13 -m pip uninstall -y --break-system-packages pip && \
    rm -f /tmp/requirements.txt /etc/shadow && \
    mkdir -p /opt/gregale/code-interpreter /workspace && \
    (id app >/dev/null 2>&1 || adduser -D -u 1000 app) && \
    chown 1000:1000 /workspace
COPY --chmod=0755 images/sandbox-code-interpreter/driver.py /opt/gregale/code-interpreter/driver.py
ENV MPLBACKEND=module://matplotlib_inline.backend_inline \
    MPLCONFIGDIR=/tmp/matplotlib
WORKDIR /workspace
