# images/ — Dockerfiles for content-addressed base images (spec §4.6, §15)

`base-minimal`, `runner-node22`, `runner-python312`, `builder-base`. Built in CI,
staged to /srv/fc/base/ (inside the 60 GB reserve, counted once). drive0 is one of
these shared read-only base rootfs; per-app layers stack over it via overlayfs.
Never flatten into one rootfs per app (breaks the 130 MB fleet target).
