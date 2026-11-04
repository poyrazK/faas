# spec: §4.6 — diagnostic platform runtime artifact, no image publication.
# This is the same immutable Alpine parent used by runner-node22. The source
# compiler runs in an ephemeral CI image build, as in images.yml (ADR-114).
FROM node:22-alpine@sha256:76789712cd1ae89a1225eac9077010d68987a423588042dac30446f502f1858c AS build
RUN apk add --no-cache bash curl xz patch make gcc g++ python3 linux-headers binutils
WORKDIR /diagnostic
COPY build.sh manifest.py framed-smoke.cjs identity-map.patch ./
COPY fixture/ ./fixture/
RUN bash /diagnostic/build.sh

FROM scratch AS artifact
COPY --from=build /diagnostic/artifact/ /
