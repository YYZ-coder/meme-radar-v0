# radar image. Default role: radar api --store=mock on :8080; compose runs the
# ingest roles from the same image with their own entrypoint (no HTTP port).
FROM golang:1.26.5-alpine AS build
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY} CGO_ENABLED=0
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /out/radar ./cmd/radar

FROM alpine:3.22
COPY --from=build /out/radar /usr/local/bin/radar
USER 65532:65532
EXPOSE 8080
HEALTHCHECK --interval=5s --timeout=2s --start-period=3s --retries=10 \
  CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["radar", "api", "--store=mock", "--addr=0.0.0.0:8080"]
