# Ядро AI IT-Buddy: сборка в golang, запуск в distroless без shell, не от root
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/root/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG VERSION=dev
RUN --mount=type=cache,target=/root/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/core ./cmd/core

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/core /usr/local/bin/core
USER nonroot:nonroot
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=5s --start-period=5s --retries=3 CMD ["/usr/local/bin/core", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/core"]
