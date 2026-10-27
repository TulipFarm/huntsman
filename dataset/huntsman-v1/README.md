---
license: cc-by-4.0
language:
- am
- ar
- az
- bg
- bn
- ckb
- cs
- da
- de
- dv
- el
- en
- es
- fa
- fi
- fr
- gu
- he
- hi
- hr
- hu
- hy
- id
- it
- ja
- ka
- kk
- km
- kn
- ko
- lo
- lt
- ml
- mn
- mr
- ms
- my
- ne
- nl
- no
- pa
- pl
- ps
- pt
- ro
- ru
- sd
- si
- sk
- sr
- sv
- sw
- ta
- te
- th
- tr
- uk
- ur
- uz
- vi
- yi
- yo
- zh
multilinguality: multilingual
size_categories:
- 1K<n<10K
task_categories:
- text-retrieval
pretty_name: Huntsman Scrape Dataset v1
tags:
- scrape
- web-scraping
- benchmark
- boilerplate-removal
configs:
- config_name: default
  data_files:
  - split: test
    path: data/test.parquet
  - split: dev
    path: data/dev.parquet
---

# Huntsman Scrape Dataset v1

A benchmark for evaluating web scraping engines on content quality, balanced across language, script direction, rendering technology, page type, media format, difficulty, region and topic. It extends Firecrawl's [scrape-content-dataset-v1](https://huggingface.co/datasets/firecrawl/scrape-content-dataset-v1) and keeps its first five columns, so tooling for that dataset works unchanged.

Built by [Huntsman](https://github.com/TulipFarm/huntsman) ([source](https://github.com/tulipfarm/huntsman)). The pipeline, quotas and design spec are in [`dataset/huntsman-v1/`](https://github.com/tulipfarm/huntsman/tree/main/dataset/huntsman-v1).

## Overview

10,000 rows, 63 languages, 226 countries, 9036 domains. Created 2026-10-27.

```python
from datasets import load_dataset

ds = load_dataset("tulipfarmai/huntsman-scrape-dataset-v1", split="test")
```

Use `dev` for tuning and report scores on `test`. `data/huntsman-scrape-dataset-v1.csv` holds both splits and works with [`eval.py`](https://github.com/tulipfarm/huntsman/blob/main/dataset/eval.py).

## Dataset Structure

- `id`: identifier
- `url`: full URL of the web page
- `truth_text`: ~100-word core snippet (main content)
- `lie_text`: ~10-word non-core snippet (navigation, footer, ads, cookie banner)
- `error`: optional error message if page retrieval failed (always empty: failed pages are dropped)
- `split`: `dev` or `test`
- `language`, `lang_tier`, `script`, `direction` (`ltr` / `rtl` / `mixed`)
- `region`, `country`
- `render_mode` (`static` / `ssr` / `csr`), `platform`
- `page_type`, `topic`
- `media`, `difficulty`: `;`-separated lists (empty difficulty = clean)
- `source` (`commoncrawl` / `tranco` / `search`), `review` (`none` / `llm_review` / `human`)
- `domain`, `final_url`, `http_status`, `fetched_at`

## How it was built

Candidates come from the Common Crawl index, the Tranco top list and targeted search for rare buckets. Each page was rendered in a headless browser, tagged by rules, labelled by an LLM (snippets are checked to occur verbatim on the page) and verified by a second LLM. Most pages were re-rendered after 24 h to drop unstable pages. Right-to-left and long-tail-language rows get an extra LLM review, and a stratified 500-row sample was spot-checked by a third LLM. No human review.

## Limitations

**Created**: 2026-10-27

- URLs may become unavailable, and content may be updated or removed
- Snippets are short excerpts of third-party pages; full page content is not included
- Scanned PDFs are excluded (no OCR)
- Rare languages may have fewer rows than their quota

## License

The dataset (labels, metadata and selection) is released under [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/). The `truth_text` and `lie_text` snippets are short excerpts of third-party web pages and remain the property of their owners; they are included for research and benchmarking. Please respect website terms of service and use appropriate rate limiting.
