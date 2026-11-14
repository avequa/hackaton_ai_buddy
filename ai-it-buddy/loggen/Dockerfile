# Генератор access-логов для демо incident-radar: только стандартная библиотека, distroless, не root
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/loggen . && mkdir /out/logs

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/loggen /usr/local/bin/loggen
# каталог логов принадлежит nonroot: именованный том при первом подключении берет владельца из образа
COPY --from=build --chown=65532:65532 /out/logs /logs
USER nonroot:nonroot
EXPOSE 8092
HEALTHCHECK --interval=10s --timeout=3s CMD ["/usr/local/bin/loggen", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/loggen"]
