# Internal Jobs CI protocol image. Build only; no publication/dispatch matrix.
# Repository commands run inside Firecracker after the guest marker/proof checks.
FROM --platform=$BUILDPLATFORM public.ecr.aws/docker/library/golang:1.26.11@sha256:f6946db66567acc1497655237e58f06ec3a57478a5e0d67fca29d2ffcf2ea136 AS build
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
