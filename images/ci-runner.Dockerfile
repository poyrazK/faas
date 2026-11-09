# Internal Jobs CI protocol image. Build only; no publication/dispatch matrix.
# Repository commands run inside Firecracker after the guest marker/proof checks.
FROM --platform=$BUILDPLATFORM public.ecr.aws/docker/library/golang:1.26.10@sha256:cfa27959837144bf4649802ba38e59daffd0dd2a7994aa83daaf113285b0f551 AS build
ARG TARGETARCH=amd64
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -trimpath -buildvcs=false -ldflags='-s -w' -o /out/gregale-ci-runner ./cmd/ci-runner

FROM public.ecr.aws/docker/library/debian:12-slim@sha256:c67e92c8099ba22853ab4cfda7cfb494d253d6012ca82dee98ba8976772af018
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/gregale-ci-runner /usr/local/bin/gregale-ci-runner
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/gregale-ci-runner", "v1"]
