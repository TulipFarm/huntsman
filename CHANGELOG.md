# Changelog

All notable changes to Huntsman. Versions follow [Semantic Versioning](https://semver.org), and each release is
published as `ghcr.io/tulipfarm/huntsman:<version>`.

## [0.2.1](https://github.com/TulipFarm/huntsman/releases/tag/v0.2.1) - 2026-10-30

### Features

- Size the worker pool to CPU and memory by default
- **dataset:** Add tulipfarm.ai pages in dataset v1.1 ([#11](https://github.com/TulipFarm/huntsman/pull/11))

### Bug fixes

- Use the server's HTML when a page's JavaScript app crashes ([#13](https://github.com/TulipFarm/huntsman/pull/13))
- Stop the scraper crashing under heavy load ([#14](https://github.com/TulipFarm/huntsman/pull/14))

### Documentation

- Link the scrape dataset on Hugging Face from the README and site ([#9](https://github.com/TulipFarm/huntsman/pull/9))

### Other changes

- Deploy to Cloudflare Pages
- Redesign the landing page around the live scrape ([#15](https://github.com/TulipFarm/huntsman/pull/15))

## [0.2.0](https://github.com/TulipFarm/huntsman/releases/tag/v0.2.0) - 2026-10-28

### Features

- Add named API keys and SearXNG search proxy ([#7](https://github.com/TulipFarm/huntsman/pull/7))

### Bug fixes

- Remove the SearXNG search proxy ([#8](https://github.com/TulipFarm/huntsman/pull/8))

### Other changes

- Custom datasets, split filter and exact-limit search
- Exact concurrency results on the huntsman-v1 sample
- Astro landing page with benchmarks and live scrape
- Document named API keys

## [0.1.2](https://github.com/TulipFarm/huntsman/releases/tag/v0.1.2) - 2026-10-27

### Dependencies

- Upgrade Go modules, chromedp 0.21 and GitHub Actions ([#6](https://github.com/TulipFarm/huntsman/pull/6))

## [0.1.1](https://github.com/TulipFarm/huntsman/releases/tag/v0.1.1) - 2026-10-27

### Build and CI

- Release from a local make release command ([#5](https://github.com/TulipFarm/huntsman/pull/5))

## [0.1.0](https://github.com/TulipFarm/huntsman/releases/tag/v0.1.0) - 2026-10-27

### Documentation

- Prepare Huntsman for open source ([#2](https://github.com/TulipFarm/huntsman/pull/2))

### Build and CI

- Publish multi-platform images to GHCR on version tags ([#1](https://github.com/TulipFarm/huntsman/pull/1))

### Other changes

- Self-hosted scraper, benchmarks and scrape dataset v1

