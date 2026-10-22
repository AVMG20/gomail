# gomail-sync: the server that keeps gomail's settings, labels and sign-ins
# in sync between computers. Build from the repository root (.dockerignore
# there keeps the context small):
#
#   docker build -f deploy/sync/Dockerfile -t gomail-sync .
#
# A static, cgo-free binary on a distroless base, running as a non-root user.

FROM golang:1.26-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS=-trimpath
COPY go.mod go.sum ./
COPY cmd/gomail-sync ./cmd/gomail-sync
COPY internal/syncapi ./internal/syncapi
COPY internal/syncserver ./internal/syncserver
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -ldflags="-s -w" -o /out/gomail-sync ./cmd/gomail-sync
# The data folder, owned by the runtime user (a new named volume copies it).
RUN mkdir -p /out/data && chown 65532:65532 /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/gomail-sync /gomail-sync
COPY --from=build --chown=65532:65532 /out/data /data
ENV ADDR=:8080 DATA_DIR=/data
EXPOSE 8080
VOLUME ["/data"]
USER 65532:65532
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/gomail-sync", "healthcheck"]
ENTRYPOINT ["/gomail-sync"]
