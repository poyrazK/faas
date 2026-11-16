# Platform-owned shared base for one-shot, networkless repair rehearsal.
# Publish immutable ext4 artifacts and configure their byte digests explicitly.
FROM postgres:16@sha256:2d5c77ae9b0e0944ffb92f8d8fc2fa2edb215cb0976da8bac34df917af417e87
COPY images/rootfs-skel/ /
RUN mkdir -p /etc/faas /repair && \
    printf '%s\n' 'postgres:x:999:999:PostgreSQL:/var/lib/postgresql:/bin/sh' >> /etc/passwd && \
    printf '%s\n' 'postgres:x:999:' >> /etc/group && \
    printf '%s\n' '{"kind":"data_repair","version":1}' > /etc/faas/data-repair.json && \
    rm -f /etc/faas/execution.json /etc/faas/app-task.json /etc/faas/job.json /etc/faas/build.json /etc/shadow
# Gregale's rootfs publisher installs the current static guest-init as /init.
# Neither the image entrypoint nor any application process is executed.
