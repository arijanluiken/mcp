FROM golang:1.22 as build
WORKDIR /src
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -o /out/mcp-server ./

FROM gcr.io/distroless/base-debian12
WORKDIR /
COPY --from=build /out/mcp-server /mcp-server
ENV MIMIR_URL=http://mimir:9009/prometheus
ENTRYPOINT ["/mcp-server"]
