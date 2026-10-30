# Internal Jobs CI protocol image. Build only; no publication/dispatch matrix.
# Repository commands run inside Firecracker after the guest marker/proof checks.
FROM --platform=$BUILDPLATFORM public.ecr.aws/docker/library/golang:1.26.10@sha256:670bb5aa5349ae03b8516764718486f4f16399134bdc9acd0c46a23f043ab05c AS build
ARG TARGETARCH=amd64
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -trimpath -buildvcs=false -ldflags='-s -w' -o /out/gregale-ci-runner ./cmd/ci-runner

FROM public.ecr.aws/docker/library/debian:12-slim@sha256:a3bba7dc7dbcc1f37c2856c51fc9a7fac96608f50cb87b1b4e4d8b8c85ae988e
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/gregale-ci-runner /usr/local/bin/gregale-ci-runner
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/gregale-ci-runner", "v1"]
