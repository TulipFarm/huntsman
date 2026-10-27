ARG LIGHTPANDA_IMAGE=lightpanda/browser@sha256:5d1d724c476957d8c2f8393a51fd089477523b0e5aa9c783c48688785d934fb9
FROM ${LIGHTPANDA_IMAGE} AS browser
# Optional: --build-context gomodcache="$(go env GOMODCACHE)" reuses host modules offline.
FROM scratch AS gomodcache
# Cross-compiles on the build machine, so multi-platform images need no emulation for Go.
FROM --platform=$BUILDPLATFORM golang:1.27-trixie AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=bind,from=gomodcache,target=/gomodcache \
    if [ -d /gomodcache/cache/download ]; then export GOPROXY=file:///gomodcache/cache/download,https://proxy.golang.org,direct; fi; \
    go mod download
COPY . .
RUN export CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    && go build -trimpath -ldflags="-s -w" -o /out/huntsman ./src/cmd/huntsman \
    && go build -trimpath -ldflags="-s -w" -o /out/bench ./benchmark/fixture/bench \
    && go build -trimpath -ldflags="-s -w" -o /out/fixture ./benchmark/fixture/server

FROM build AS test
COPY --from=browser /bin/lightpanda /usr/local/bin/lightpanda
RUN HUNTSMAN_TEST_BROWSER=/usr/local/bin/lightpanda go test -race -count=1 ./...

FROM debian:trixie-slim AS runtime
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates libstdc++6 \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --uid 10001 --create-home huntsman \
    && mkdir /data && chown huntsman /data
COPY --from=browser /bin/lightpanda /usr/local/bin/lightpanda
COPY --from=build /out/ /usr/local/bin/
# Ad, tracker, and cookie-banner filters (EasyList project, GPLv3 / CC BY-SA 3.0), fetched at build time.
ADD https://easylist.to/easylist/easylist.txt https://easylist.to/easylist/easyprivacy.txt \
    https://secure.fanboy.co.nz/fanboy-cookiemonster.txt /usr/share/huntsman/filters/
RUN chmod -R a+rX /usr/share/huntsman
ENV LIGHTPANDA_DISABLE_TELEMETRY=true \
    HUNTSMAN_ADBLOCK_LISTS=/usr/share/huntsman/filters/easylist.txt,/usr/share/huntsman/filters/easyprivacy.txt,/usr/share/huntsman/filters/fanboy-cookiemonster.txt \
    LIGHTPANDA_DISABLE_CORE_DUMP=true \
    HUNTSMAN_ADDR=0.0.0.0:8080 \
    HUNTSMAN_DB=/data/huntsman.db
USER huntsman
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["huntsman"]
