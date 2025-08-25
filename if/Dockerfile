# syntax=docker/dockerfile:1

FROM golang:1.22 AS build
WORKDIR /src
COPY go.mod ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY internal ./internal
COPY main.go ./
RUN --mount=type=cache,target=/go/pkg/mod CGO_ENABLED=0 go build -o /out/if-service ./

FROM gcr.io/distroless/base-debian12:latest
WORKDIR /app
COPY --from=build /out/if-service /app/if-service
USER nonroot:nonroot
EXPOSE 9030
ENTRYPOINT ["/app/if-service"]
