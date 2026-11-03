FROM golang:1.27.1 AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/healthcheck-server ./cmd/healthcheck-server

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/healthcheck-server /healthcheck-server
EXPOSE 8080
# Bind to all interfaces inside the container; restrict exposure with `docker run -p 127.0.0.1:8080:8080`.
ENTRYPOINT ["/healthcheck-server", "-addr", "0.0.0.0:8080"]
