# Huntsman

A small, self-hosted web scraper that turns any page into clean Markdown. It renders JavaScript with
[Lightpanda](https://github.com/lightpanda-io/browser), strips ads, trackers and cookie banners, and exposes a
Firecrawl-compatible `/v1/scrape` API.

Website: [huntsman.tulipfarm.ai](https://huntsman.tulipfarm.ai)

## Quick start

Run the published image (`linux/amd64` and `linux/arm64`):

```sh
docker run -d -p 127.0.0.1:8080:8080 -v huntsman-data:/data ghcr.io/tulipfarm/huntsman:latest
curl -s localhost:8080/v1/scrape -H 'Content-Type: application/json' -d '{"url":"https://example.com"}'
```

Pin a version such as `ghcr.io/tulipfarm/huntsman:0.1.0`, or its digest, in production. To build from source instead:

```sh
docker compose up -d --build
curl -s localhost:8080/v1/scrape -H 'Content-Type: application/json' -d '{"url":"https://example.com"}'
```

## API

| Endpoint | What it does |
|---|---|
| `POST /v1/scrape` | Scrape one URL and return `markdown` and `links` |
| `POST /v1/batch` | Queue 1–1000 URLs: `{"urls": [...], "scrapeOptions": {...}}` |
| `GET /v1/jobs/{id}` | Status and result of a queued job |
| `POST /v1/crawl` | Crawl a site: `{"url", "limit", "maxDepth", "scrapeOptions"}` |
| `GET /v1/crawl/{id}?offset=N` | Crawl progress and pages |
| `GET /health` | Liveness and storage check |

Scrape options: `url`, `formats` (only `markdown`), `waitFor` (ms), `waitSelector`, `timeout` (ms), `maxAge`
(ms, cache) and `onlyMainContent`. If `HUNTSMAN_TOKEN` is set, every endpoint except `/health` needs
`Authorization: Bearer <token>`.

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `HUNTSMAN_ADDR` | `127.0.0.1:8080` (`0.0.0.0:8080` in Docker) | Listen address |
| `HUNTSMAN_WORKERS` | `4` | Browser instances, i.e. pages rendered at once |
| `HUNTSMAN_PER_HOST` | `2` | Concurrent pages per host |
| `HUNTSMAN_QUEUE_CAPACITY` | `10000` | Queued jobs before `429` |
| `HUNTSMAN_CACHE_TTL` | `1h` | Default result cache age (max `168h`) |
| `HUNTSMAN_TOKEN` | empty | Bearer token; empty disables auth |
| `HUNTSMAN_OBEY_ROBOTS` | `true` | Respect `robots.txt` |
| `HUNTSMAN_ALLOW_PRIVATE` | `false` | Allow private and loopback addresses |
| `HUNTSMAN_IDLE_MS` | `200` | Network-idle wait before extracting |
| `HUNTSMAN_ADBLOCK_LISTS` | EasyList, EasyPrivacy, Fanboy Cookiemonster in Docker | Comma-separated filter list files |
| `HUNTSMAN_DB` | `data/huntsman.db` | SQLite queue and cache |
| `HUNTSMAN_BROWSER` | `lightpanda` | Browser binary |
| `HUNTSMAN_BROWSER_PORT` | `9222` | First CDP port; worker *i* uses port + *i* |

## Repository layout

```
src/         the scraper: cmd/huntsman (server) and internal/engine
benchmark/   speed, memory and accuracy against Firecrawl, Crawl4AI and Spider (see benchmark/README.md)
dataset/     URL benchmark datasets and eval.py (see dataset/README.md)
```

## Development

Requires Go and, for browser tests, a Lightpanda binary.

```sh
go build ./...
go test ./...                                              # browser tests are skipped without a browser
HUNTSMAN_TEST_BROWSER=/path/to/lightpanda go test ./...
docker build --target test .                               # full test suite with Lightpanda inside Docker
```

## Releases

Pushing a `vX.Y.Z` tag runs the Docker test suite, then publishes `ghcr.io/tulipfarm/huntsman` for `linux/amd64` and
`linux/arm64` with tags `X.Y.Z`, `X.Y` and `latest`, a provenance attestation and an SBOM.

```sh
git tag v0.1.0 && git push origin v0.1.0
```

See [CONTRIBUTING.md](CONTRIBUTING.md) to contribute and [SECURITY.md](SECURITY.md) to report a vulnerability.

## License

[Apache-2.0](LICENSE). The Docker image downloads the EasyList filter lists at build time; those lists keep their own
licenses (GPLv3 / CC BY-SA 3.0). The Huntsman scrape dataset has its own license, see
[dataset/huntsman-v1/README.md](dataset/huntsman-v1/README.md).
