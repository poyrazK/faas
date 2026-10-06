# Build from the repository root, with this Dockerfile selected by the
# staging fixture's source deployment. Gregale's builder owns image building.
FROM golang:1.25.13-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/consumer ./scripts/ops/eventdelivery-consumer

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/consumer /consumer
ENV PORT=8080
EXPOSE 8080
ENTRYPOINT ["/consumer"]
