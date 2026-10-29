# Benchmarks

Everything used to measure Huntsman against other self-hosted scrapers lives here. The URL datasets and `eval.py`, which scores Markdown output with the same rules as Firecrawl's scrape-evals (MIT), live in [`../dataset/`](../dataset/).

| Path | What it is |
|---|---|
| `fixture/` | Local fixture site plus a load generator for a controlled Huntsman vs Firecrawl comparison |
| `engines/spider/` | A small HTTP wrapper that exposes [spider-rs](https://github.com/spider-rs/spider) as a `/v1/scrape` endpoint |
| `cmd/bench/` | The `bench` CLI: creates the VMs, runs the concurrency step-up on them, streams progress, saves results, refreshes the report |
| `aws/` | Pulumi project for the three benchmark machines |
| `results/` | Output (git-ignored, except where noted in the report) |

## Engines

| Engine | Image | Concurrency setting at level N |
|---|---|---|
| Huntsman | built from this repo's `Dockerfile` | `HUNTSMAN_WORKERS=N` |
| Firecrawl | `fixture/Dockerfile.firecrawl`: the public `ghcr.io/firecrawl/*` images (API, Playwright service, NuQ Postgres) bundled with Postgres, Redis and RabbitMQ into one container | `NUQ_WORKER_COUNT=1`, `MAX_CONCURRENT_PAGES=max(N, 10)`. `/v1/scrape` runs inside the API process; NuQ workers only serve crawls, so extra workers would sit idle. `--firecrawl-scrape-only` skips the harness and starts only the API and browser service (about 1.5 GB less memory); the small-machine results use it because the full stack runs out of memory there |
| Crawl4AI | `unclecode/crawl4ai:latest`, `/crawl` endpoint with the pruning (`fit`) Markdown filter, so the site's HTTP status is scored as in scrape-evals | `crawler.pool.max_pages: N` in `config.yml`, API rate limit raised |
| Spider | `engines/spider/` (spider-rs with one shared headless Chromium) | `SPIDER_CONCURRENCY=N` |

Each engine renders JavaScript with a real browser: Lightpanda for Huntsman, Chromium for the others. Caches are bypassed so every request is a fresh render.

## What a run measures

Each engine runs two phases on each machine. Every level starts a fresh container, configured for N concurrent pages and limited to host RAM minus 768 MB. CPU and memory are sampled every second (cgroup v2, or `docker stats`), and the biggest processes every 10 s.

### 1. Capacity: how many pages at once, and how accurate

For each level N (8, 16, 32, 64, 128, 256, 512 by default; set with `--levels`), send every dataset URL (1,000 by default) with exactly N requests in flight and a 30 s page timeout, then score the Markdown with the scrape-evals rules.

The ramp stops at the first level where the engine crashes or is OOM-killed, success drops more than 10 points below the best level, median latency exceeds 3× the first level's median, or throughput falls below 80% of the best level. **Max sustainable concurrency** is the highest level that passed.

Firecrawl starts at level 1 (levels 1–64, 900 s start timeout, latency baseline from level 8) because it does not reach 8 on the smaller machines. Its Playwright page pool is kept at 10 or more: the Playwright service holds a page slot after a hung navigation, and with exactly N slots it gradually stops rendering.

### 2. Resource profile: CPU and memory at 1, 2, 3 … pages at once

Levels 1–8, then 12, 16, 24, 32, 48, 64, 96, 128 … up to the capacity limit plus one level beyond it, so the profile shows where the engine breaks. Each level keeps N requests in flight for 90 s (at least 20 URLs), going through the dataset in the same order every time. The first 20 s are warm-up: CPU, average memory and pages/s cover the remaining 70 s; peak memory covers the whole run. The profile stops early if the engine crashes or runs out of memory.

`--phases capacity` or `--phases profile` runs one phase; a profile-only run reads the limit from the previous capacity results. `--profile-levels`, `--profile-seconds` and `--profile-warmup` change the profile.

## The `bench` CLI

Run every command from the repository root. `go run ./benchmark/cmd/bench help` lists them; `<command> -h` shows flags.

### On AWS

Requirements: Go, Docker with arm64 build support, Pulumi, and AWS credentials in the environment (`AWS_PROFILE` or keys).

```sh
# 1. Create the machines. Writes their addresses to benchmark/.env and waits until Docker is ready.
go run ./benchmark/cmd/bench provision

# 2. Run. For huntsman-v1, fetch its data first: hf download tulipfarmai/huntsman-scrape-dataset-v1 --repo-type dataset --include "data/*" --local-dir dataset/huntsman-v1
# Then run. Output streams live, prefixed by machine; results land in benchmark/results/matrix/<machine>/<engine>/.
go run ./benchmark/cmd/bench run --machine all --engine all
go run ./benchmark/cmd/bench run --machine small --engine firecrawl --levels 1,2,4
go run ./benchmark/cmd/bench run --machine small --engine huntsman --phases profile
go run ./benchmark/cmd/bench run --machine all --engine all \
  --dataset dataset/huntsman-v1/data/huntsman-scrape-dataset-v1.csv --split test

# 3. Look at the numbers, then refresh the HTML report.
go run ./benchmark/cmd/bench report
go run ./benchmark/cmd/bench report --inject benchmark/results/index.html

# 4. Delete the machines.
go run ./benchmark/cmd/bench destroy
```

| Machine | Instance | vCPU | RAM | Disk |
|---|---|---|---|---|
| small | c7g.large | 2 | 4 GB | 30 GB gp3 |
| medium | c7g.xlarge | 4 | 8 GB | 40 GB gp3 |
| large | c7g.2xlarge | 8 | 16 GB | 60 GB gp3 |

All three are Graviton (arm64) Ubuntu 24.04. SSH is open only to your public IP. Pulumi state and the SSH key stay in `benchmark/aws/.state/`.

`run` uploads the CLI and dataset (`--dataset`, default Firecrawl's 1,000 URLs; `--split` keeps only rows with that `split` value), builds or ships the engine image, and starts the ramp detached on each machine. Machines run in parallel; engines run one after another. Every 10 s it prints progress: URLs done, success so far, pages/s, CPU and memory. Ctrl-C only detaches; the ramp keeps going on the machine.

| Command | Use |
|---|---|
| `status` | What each machine is running |
| `attach --machine all` | Stream the output of running ramps again |
| `fetch --machine all` | Copy results now (an unfinished ramp is saved as `<engine>.partial`) |
| `stop --machine small` | Stop a ramp and its container |
| `ssh --machine small` | Shell on a machine |

Per level (`level-N/` for capacity, `profile/level-N/` for the profile), the results directory has `level.json` (summary and verdict), `resources.jsonl` (CPU and memory per second), `processes.jsonl` (top processes every 10 s), `engine.log` and `results.jsonl` (each URL's score). `<engine>.log` next to it is the full console output.

### On other machines

Any Linux host with Docker and SSH works. Write `benchmark/.env` by hand (see `.env.example`); a host may include a port (`10.0.0.5:2222`).

### On this machine

```sh
go run ./benchmark/cmd/bench ramp --engine huntsman --limit 50 --levels 4,8
```

It uses the local image (`huntsman:bench`, `firecrawl-bench`, `spider-bench` or `unclecode/crawl4ai`) and writes to `benchmark/results/matrix/local/<engine>/`.
