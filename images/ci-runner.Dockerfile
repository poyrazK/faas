# Internal Jobs CI protocol image. Build only; no publication/dispatch matrix.
# Repository commands run inside Firecracker after the guest marker/proof checks.
FROM --platform=$BUILDPLATFORM public.ecr.aws/docker/library/golang:1.26.9@sha256:f1f0bcc2c524a3ced375fcb4d1ecb7aa371aa7070e112599aaca45cc02d0101b AS build
ARG TARGETARCH=amd64
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -trimpath -buildvcs=false -ldflags='-s -w' -o /out/gregale-ci-runner ./cmd/ci-runner

FROM public.ecr.aws/docker/library/debian:12-slim@sha256:a4672c0cb26fbdde88e38fa2dfb6c681942306680e41e4378b28770b6e79ee91
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/gregale-ci-runner /usr/local/bin/gregale-ci-runner
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/gregale-ci-runner", "v1"]
