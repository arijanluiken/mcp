FROM golang:1.22 AS build
WORKDIR /src
COPY go.mod ./
COPY go.sum ./
COPY internal ./internal
COPY service-a ./service-a
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd service-a && go build -o /out/service-a

FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=build /out/service-a /app/service-a
EXPOSE 8080
ENV OTEL_EXPORTER_OTLP_ENDPOINT=otel-collector:4317
ENTRYPOINT ["/app/service-a"]
