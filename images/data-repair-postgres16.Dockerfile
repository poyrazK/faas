# Platform-owned shared base for one-shot, networkless repair rehearsal.
# Publish immutable ext4 artifacts and configure their byte digests explicitly.
FROM postgres:16@sha256:b95b87e93ceb7594715d5c71fb6e09f6fc878851ab537735d617325eb9ba8b1b
COPY images/rootfs-skel/ /
RUN mkdir -p /etc/faas /repair && \
    printf '%s\n' 'postgres:x:999:999:PostgreSQL:/var/lib/postgresql:/bin/sh' >> /etc/passwd && \
    printf '%s\n' 'postgres:x:999:' >> /etc/group && \
    printf '%s\n' '{"kind":"data_repair","version":1}' > /etc/faas/data-repair.json && \
    rm -f /etc/faas/execution.json /etc/faas/app-task.json /etc/faas/job.json /etc/faas/build.json /etc/shadow
# Gregale's rootfs publisher installs the current static guest-init as /init.
# Neither the image entrypoint nor any application process is executed.
