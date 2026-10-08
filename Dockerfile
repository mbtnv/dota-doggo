# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.26.5-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
    -trimpath -buildvcs=false -ldflags='-s -w' -o /out/dota-doggo ./cmd/dota-doggo

FROM build AS fixture-build
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
    -trimpath -buildvcs=false -ldflags='-s -w' -o /out/fixture-api ./cmd/fixture-api

FROM scratch AS fixture-api
COPY --from=fixture-build /out/fixture-api /fixture-api
COPY testdata/opendota-recent.json /fixtures/opendota-recent.json
USER 65532:65532
ENTRYPOINT ["/fixture-api"]

FROM scratch AS runtime
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/dota-doggo /dota-doggo
USER 65532:65532
ENV HEALTH_ADDR=127.0.0.1:8080
ENTRYPOINT ["/dota-doggo"]
CMD ["--help"]
