# huntsman-scrape-dataset-v1 — design spec

Goal: a 10,000-URL benchmark that tests whether a scraper captures a page's core content and leaves out noise,
balanced across language, script direction, rendering tech, page type, media format, difficulty, region and topic.

Every row is labelled and verified by LLMs, and a sample is reviewed (see §3).

Firecrawl's scrape-content-dataset-v1 (1,000 rows) is mostly English-adjacent, HTML-only, unlabelled by dimension, and 13% of rows are fetch failures.
This dataset fixes that with explicit quotas, per-row dimension labels, and validation of every label.

## 1. Output schema

Superset of Firecrawl's v1 columns, so `eval.py` keeps working unchanged on the first five columns.
Multi-valued columns are `;`-separated.

| column | example | notes |
|---|---|---|
| `id` | `4217` | |
| `url` | `https://…` | |
| `truth_text` | ~100 words | verbatim, contiguous, from main content |
| `lie_text` | ~10 words | verbatim, from boilerplate (nav, footer, ads, cookie banner) |
| `error` | | always empty; failed fetches are dropped, not shipped |
| `split` | `test` | `dev` (1,000) / `test` (9,000), stratified |
| `language` | `ar` | ISO 639-1/3 of main content |
| `script` | `Arab` | ISO 15924 |
| `direction` | `rtl` | `ltr` / `rtl` / `mixed` (RTL page with substantial LTR runs: code, numbers, Latin) |
| `region` | `MENA` | publisher region |
| `country` | `EG` | ISO 3166-1, `XX` if unknown |
| `page_type` | `news_article` | 18 types, see §2.4 |
| `topic` | `health` | 18 topics, see §2.7 |
| `render_mode` | `csr` | `static` / `ssr` / `csr` |
| `platform` | `nextjs` | CMS/framework fingerprint, `unknown` allowed |
| `media` | `pdf` / `html;tables;code` | see §2.5 |
| `difficulty` | `cookie_banner;lazy_load` | see §2.6, empty = clean baseline |
| `source` | `commoncrawl` | `commoncrawl` / `tranco` / `search` / `tulipfarm` |
| `fetched_at` | ISO 8601 | when the truth was captured |
| `http_status` | `200` | final status after redirects |
| `review` | `human_ok` | `llm` / `llm_review` / `human_ok` / `human_fixed` |

## 2. Coverage dimensions and quotas

Quotas are **minimums per bucket** (marginal), not a full cross-product. Two joint constraints are enforced:
every language tier × every page_type with ≥ 5 rows, and every render_mode × every region with ≥ 50 rows.

### 2.1 Language and script direction (sums to 10,000)

| tier | languages | per language | total |
|---|---|---|---|
| English | en | 2,000 | 2,000 |
| Major LTR | zh, es, fr, de, ja, ru, pt, hi, ko, it, tr, id, vi | 300 | 3,900 |
| RTL | ar 500, he 250, fa 250, ur 200 | — | 1,200 |
| Mid LTR | nl, pl, uk, sv, th, bn, ta, el, cs, ro, hu, ms, sw, te, mr | 120 | 1,800 |
| Long tail | ≥ 30 more (ka, hy, am, km, my, si, ne, kk, uz, az, lo, mn, yo, ha, zu, ps, sd, ckb, yi, dv, …) | ~35 | 1,100 |

- RTL ≥ 12% overall, including ≥ 150 `mixed` direction pages and ≥ 60 rows from the long-tail RTL languages (ps, sd, ckb, yi, dv).
- Scripts covered: Latn, Cyrl, Arab, Hebr, Hans/Hant, Jpan, Hang, Deva, Beng, Taml, Telu, Thai, Grek, Geor, Armn, Ethi, Khmr, Mymr, Sinh, Laoo, Thaa.
- Language is detected on rendered main text (fastText `lid.176`), cross-checked with `<html lang>`; disagreements go to LLM check.

### 2.2 Region (publisher location; ≥ 80 countries)

Europe 2,000 · Asia 2,500 · North America 1,800 · MENA 1,000 · Latin America 1,000 · Sub-Saharan Africa 600 · Oceania 300
(remaining 800 float). Signal order: ccTLD → page content (address, currency, phone format) → LLM.
Max 15% of rows from any single country.

### 2.3 Rendering technology

| render_mode | min | definition |
|---|---|---|
| `static` | 3,000 | raw HTML text ≈ rendered text, no hydration framework |
| `ssr` | 2,500 | framework present, raw HTML already holds main content |
| `csr` | 1,500 | raw HTML holds < 10% of rendered main text (empty shell) |

Platforms (min 150 each): WordPress, Shopify, Wix, Squarespace, Webflow, Drupal, Ghost, Next.js, Nuxt, Gatsby/Astro,
Angular, plain React/Vue SPA, Docusaurus/MkDocs/Sphinx/GitBook, AMP, MediaWiki, Discourse/phpBB.
Detected with Wappalyzer-style fingerprints plus the raw-vs-rendered text ratio.

### 2.4 Page type (min 200 each, max 15% each)

news_article, blog_post, docs_tutorial, product_page, listing_category, homepage_landing, company_info, forum_qa,
reference_wiki, academic_research, government_org, job_listing, event, profile_directory, media, legal_policy,
login_error (login walls only — see §2.6), other (≤ 3%).

### 2.5 Media / format (minimums; a row can carry several)

| tag | min | | tag | min |
|---|---|---|---|---|
| `pdf` | 600 | | `video` (embedded player) | 400 |
| `office` (docx/pptx/xlsx) | 200 | | `audio` / podcast | 150 |
| `tables` (main content is tabular) | 600 | | `image_heavy` (gallery, captions) | 500 |
| `code` (code blocks in main) | 600 | | `long_form` (> 5,000 words) | 500 |
| `math` (MathJax/KaTeX/MathML) | 150 | | `iframe_content` (main content in iframe) | 150 |

PDFs are sampled across languages too: ≥ 150 non-English and ≥ 50 RTL PDFs. Scanned PDFs (no text layer) are dropped: the pipeline has no OCR, so it
cannot make ground truth for them.

### 2.6 Difficulty (minimums; empty = clean baseline, ≥ 3,000 rows)

| tag | min | detection |
|---|---|---|
| `cookie_banner` | 1,500 | CMP fingerprints (OneTrust, Cookiebot, Didomi, Quantcast, TCF API) |
| `lazy_load` / `infinite_scroll` | 500 | rendered text grows ≥ 20% after scrolling |
| `anti_bot` | 400 | Cloudflare/Akamai/DataDome/PerimeterX challenge on plain fetch |
| `paywall` | 300 | metered/soft paywall markers, `isAccessibleForFree: false` |
| `login_wall` | 200 | core content hidden behind sign-in prompt |
| `geo_redirect` | 200 | locale redirect or different content by geo |
| `shadow_dom` | 200 | main text inside shadow roots / web components |
| `non_utf8` | 200 | Shift_JIS, GBK, Windows-1256, KOI8-R, etc. |
| `huge_dom` | 150 | > 5 MB DOM or > 20k nodes |
| `redirect_chain` | 150 | ≥ 3 hops |

For `anti_bot`, `paywall` and `login_wall`, truth is labelled from what a normal browser user sees without logging in.
The benchmark measures if the scraper gets what a human visitor gets.

### 2.7 Topic (min 200 each, IAB-style tier 1)

technology, business_finance, health, science, education, government_politics, news_current_events, shopping_retail,
travel, food_drink, sports, entertainment, arts_culture, religion, law, real_estate, automotive, careers.

### 2.8 Publisher pages (v1.1)

Source `tulipfarm` holds every page listed in the tulipfarm.ai sitemaps, the site of Huntsman's publisher, so
Huntsman is benchmarked on all of it. tulipfarm.ai is in `max_per_domain_exempt` in `quotas.yaml`, so it is exempt
from the 3-URLs-per-domain limit. The pages don't state a location, so `10_extend.py` sets their country to `US`
and region to North America.

## 3. Pipeline

```
 ┌────────────┐   ┌──────────┐   ┌───────────┐   ┌──────────┐   ┌──────────┐   ┌──────────┐   ┌────────┐
 │1 candidates│──▶│ 2 fetch  │──▶│3 auto-tag │──▶│4 LLM     │──▶│5 validate│──▶│6 select  │──▶│7 review│──▶ export
 │ ~35k URLs  │   │raw+render│   │lang,render│   │type,topic│   │ snippets │   │ quotas → │   │ 5% human│
 └────────────┘   └──────────┘   │media,diff │   │truth,lie │   └──────────┘   │  10,000  │   └────────┘
       ▲                         └───────────┘   └──────────┘                  └────┬─────┘
       └──────────────── gap report: buckets under quota → targeted search ─────────┘
```

1. **Candidates** (`01_candidates.py`), aim for ~3.5× the final size because of attrition:
   - Common Crawl columnar index (latest crawl, DuckDB over the public parquet) sampled by `content_languages`,
     `url_host_tld` and `content_mime_detected`. This covers most of language, region and PDF/office.
   - Tranco top 1M for popular, defended sites (anti-bot, paywall, CSR apps).
   - Local Firecrawl search (`127.0.0.1:3002/v1/search`, SearXNG) per under-filled bucket (e.g. "site:.eg وزارة", "docs mkdocs", "podcast episode transcript").
   - Rules: ≤ 3 URLs per registered domain, no URLs from Firecrawl's dataset, robots.txt respected, adult/gambling/malware dropped
     via the UT1 blocklists.
2. **Fetch** (`02_fetch.py`): plain HTTP fetch and Playwright render (scroll to bottom, wait for network idle).
   Keep raw/rendered text in a working cache while processing. Only rows selected in step 6 keep rendered HTML and
   a screenshot in `out/snapshots/` (private, not published).
3. **Auto-tag** (`03_tag.py`): language, script, direction, render_mode, platform, media and difficulty from
   deterministic signals (§2). No LLM at this stage.
4. **LLM label** (`04_label.py`): input is the rendered DOM as numbered text blocks with DOM context (no screenshot; screenshots are kept for
   human review only). Output: page_type,
   topic, region/country, the main-content block ids, one `truth_text` (~100 words, contiguous, from main blocks)
   and one `lie_text` (~10 words, from boilerplate blocks).
   No scraper/extractor under test (Firecrawl, Huntsman, Trafilatura, Readability) is used to make labels.
   This keeps the benchmark from favouring any engine.
5. **Validate** (`05_validate.py`): drop the row if any check fails:
   - truth and lie are exact substrings of the rendered text after normalisation.
   - truth is not in boilerplate, and lie is not in main content.
   - truth has ≥ 60 tokens (CJK/Thai counted per character group), and lie has ≥ 5 tokens.
   - a second, different LLM agrees with page_type and with the main/boilerplate split.
   - the page is re-fetched 24h later and truth is still present, so pages that change all the time are rejected.
6. **Select** (`06_select.py`): greedy quota filling. Fill the rarest under-quota bucket first, then break ties by
   domain diversity. It writes a gap report. Buckets still under quota go back to step 1 as targeted searches,
   repeated until every bucket meets its minimum or 3 rounds have run.
7. **Human review** (`07_review/`): 500 rows (5%), stratified so every bucket of every dimension has ≥ 5 reviewed.
   Use a small local review page with the screenshot and both snippets highlighted. Choices: ok / fix / reject.
   If a bucket has more than 10% fix+reject, relabel that bucket and review it again.
   Target: ≥ 95% ok overall. RTL and long-tail rows get a Copilot LLM review instead (see §7).
8. **Export** (`08_export.py`): `data/huntsman-scrape-dataset-v1.csv` (both splits), `data/dev.parquet` and
   `data/test.parquet`, `README.md` (the Hugging Face dataset card) and a datasheet (counts per bucket,
   review agreement, known gaps). Only URLs, snippets and metadata are published, like Firecrawl's dataset. Snapshots stay private.

## 4. Eval changes

- `eval.py --dataset <csv>` (default stays Firecrawl's dataset).
- Write `breakdown.json` with success / recall / precision / f1 per bucket of every dimension, so results show
  where an engine fails (e.g. "csr + rtl + cookie_banner").
- No change to scoring. The window-F1 logic stays the same as Firecrawl's, so scores on both datasets are comparable.

## 5. Maintenance

`09_liveness.py` re-renders all rows monthly and flags rows whose truth is gone. Replacement rows come from the same
buckets. Each refresh is a new dataset version (v1.1, v1.2, …), and every row keeps its `fetched_at`.

`10_extend.py --sources …` builds a new version from the previous one. It adds validated rows from the named
sources with ids after the current maximum (about 10% to `dev`), and removes one previous row per added row in the
same split, from the most over-filled buckets. No bucket that the previous version met may fall below its minimum,
and human-reviewed rows are never removed. It writes `out/v<version>_added.csv` and `out/v<version>_dropped.csv`.
Links to the dataset always point at its main page, never at a version tag.

### Versions

- **1.0** (2026-10-27): first release.
- **1.1** (2026-10-29): added 320 `tulipfarm` rows (§2.8) and removed 320 English v1 rows, keeping 10,000 rows.
  A scan for JavaScript crash screens also ran: `01_candidates.py crash-seeds` picked pages of Tranco sites built with
  a JavaScript framework and `crash-scan` scraped 4,634 of them with Huntsman 0.2.0. Nine showed a crash screen, all
  already in v1, so no `crash_scan` rows were added.

## 6. Layout

```
dataset/huntsman-v1/
  README.md        Hugging Face dataset card (generated)
  DATASHEET.md     counts per bucket (generated)
  SPEC.md          this file
  quotas.yaml      every minimum/maximum above, read by 06_select.py
  data/            the CSV and dev/test parquet (generated)
  pipeline/        01_candidates.py … 10_extend.py, run.sh
  review/          review sample and report; local review UI (index.html, gitignored)
  out/             candidates, cache, snapshots, state.db (gitignored)
```

## 7. Decisions

- **LLMs** run through GitHub Copilot (`copilot -p … --model …`, non-interactive):
  labeller `claude-sonnet-5.5`, verifier `gpt-5.6-terra`.
- **Search** (step 1 gap filling) uses the local self-hosted Firecrawl at `http://127.0.0.1:3002/v1/search`,
  which queries the local SearXNG at `:8888`. No cloud API key is needed.
- **Snapshots** are lean and local: rendered HTML and a screenshot only for the 10k kept rows (~20–40 GB) in
  `out/snapshots/`. Candidates that are dropped keep only their tags and labels, with no page bodies. No WARCs.
- **Review**: the user reviews English and other languages they can read. RTL and long-tail rows in the 5% sample
  get a Copilot LLM review pass (`claude-opus-5.5`, not the labeller or verifier) in the same review format.
  Those rows are marked `review = llm_review` and are counted apart from the human agreement rate.
