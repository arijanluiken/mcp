FROM golang:1.22 AS build
WORKDIR /src
COPY go.mod ./
COPY go.sum ./
COPY internal ./internal
COPY service-b ./service-b
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd service-b && go build -o /out/service-b

FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=build /out/service-b /app/service-b
EXPOSE 8081
ENV OTEL_EXPORTER_OTLP_ENDPOINT=otel-collector:4317
ENTRYPOINT ["/app/service-b"]
