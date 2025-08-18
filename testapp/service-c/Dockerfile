FROM golang:1.22 AS build
WORKDIR /src
COPY go.mod ./
COPY go.sum ./
COPY internal ./internal
COPY service-c ./service-c
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd service-c && go build -o /out/service-c

FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=build /out/service-c /app/service-c
EXPOSE 8082
ENV OTEL_EXPORTER_OTLP_ENDPOINT=otel-collector:4317
ENTRYPOINT ["/app/service-c"]
