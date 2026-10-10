# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.27.2-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/ ./cmd/chatserver ./cmd/loadbalancer

FROM gcr.io/distroless/static-debian13:nonroot AS chatserver
COPY --from=build /out/chatserver /chatserver
ENV CHAT_LISTEN=0.0.0.0:8080
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/chatserver"]

FROM gcr.io/distroless/static-debian13:nonroot AS loadbalancer
COPY --from=build /out/loadbalancer /loadbalancer
EXPOSE 9000
USER nonroot:nonroot
ENTRYPOINT ["/loadbalancer"]
