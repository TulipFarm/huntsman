# Datasets

URL datasets for measuring how well a scraper captures a page's main content and leaves out noise, plus the scorer.

| Path | What it is |
|---|---|
| `eval.py` | Scrapes every URL through a `/v1/scrape` API and scores the Markdown with the window-F1 rules of Firecrawl's [scrape-evals](https://github.com/firecrawl/scrape-evals). Datasets with dimension columns also get a per-dimension breakdown |
| `firecrawl-v1/` | Firecrawl's [scrape-content-dataset-v1](https://huggingface.co/datasets/firecrawl/scrape-content-dataset-v1): 1,000 URLs, mostly English |
| `huntsman-v1/` | [huntsman-scrape-dataset-v1](https://huggingface.co/datasets/tulipfarmai/huntsman-scrape-dataset-v1): 10,000 URLs balanced across 63 languages, script direction, rendering, page type, media, difficulty, region and topic, plus the pipeline that builds it |

```sh
python3 dataset/eval.py --engine huntsman --endpoint http://127.0.0.1:8080 --out benchmark/results/dataset/huntsman
python3 dataset/eval.py --engine huntsman --endpoint http://127.0.0.1:8080 --out benchmark/results/dataset/huntsman-v1 \
  --dataset dataset/huntsman-v1/data/huntsman-scrape-dataset-v1.csv --split test
```

To rebuild `huntsman-v1`, see [huntsman-v1/SPEC.md](huntsman-v1/SPEC.md) and `huntsman-v1/pipeline/run.sh`.
