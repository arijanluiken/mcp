FROM golang:1.22 AS build
WORKDIR /src
COPY go.mod ./
COPY go.sum ./
COPY internal ./internal
COPY service-d ./service-d
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd service-d && go build -o /out/service-d

FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=build /out/service-d /app/service-d
EXPOSE 8083
ENV OTEL_EXPORTER_OTLP_ENDPOINT=otel-collector:4317
ENTRYPOINT ["/app/service-d"]
