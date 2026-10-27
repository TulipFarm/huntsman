FROM rust:1-trixie AS build
WORKDIR /src
COPY Cargo.toml Cargo.lock ./
COPY src src
RUN --mount=type=cache,target=/usr/local/cargo/registry --mount=type=cache,target=/src/target \
    cargo build --release --locked && cp target/release/spider-scrape /spider-scrape

FROM debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends chromium ca-certificates fonts-liberation tini \
    && rm -rf /var/lib/apt/lists/* && useradd --create-home spider
COPY --from=build /spider-scrape /usr/local/bin/spider-scrape
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
USER spider
ENV CHROME_URL=http://127.0.0.1:9222/json/version
EXPOSE 8080
ENTRYPOINT ["tini", "--", "entrypoint.sh"]
