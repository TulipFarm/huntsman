"""Shared state, tables and helpers for the huntsman-scrape-dataset-v1 pipeline."""
import gzip
import hashlib
import json
import re
import sqlite3
import subprocess
import tempfile
import unicodedata
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / "out"
CACHE = OUT / "cache"
SNAPSHOTS = OUT / "snapshots"
DB_PATH = OUT / "state.db"
QUOTAS = yaml.safe_load((ROOT / "quotas.yaml").read_text())
FIRECRAWL_CSV = ROOT.parent / "firecrawl-v1" / "scrape-content-dataset-v1.csv"

LABEL_MODEL = "claude-sonnet-5.5"
VERIFY_MODEL = "gpt-5.6-terra"
REVIEW_MODEL = "claude-opus-5.5"

PAGE_TYPES = ["news_article", "blog_post", "docs_tutorial", "product_page", "listing_category", "homepage_landing",
              "company_info", "forum_qa", "reference_wiki", "academic_research", "government_org", "job_listing",
              "event", "profile_directory", "media", "legal_policy", "login_error", "other"]
TOPICS = ["technology", "business_finance", "health", "science", "education", "government_politics",
          "news_current_events", "shopping_retail", "travel", "food_drink", "sports", "entertainment",
          "arts_culture", "religion", "law", "real_estate", "automotive", "careers"]

# language -> (ISO 639-3 as used by Common Crawl's CLD2 field, tier)
LANGS = {
    "en": ("eng", "english"),
    **{k: (v, "major_ltr") for k, v in {"zh": "zho", "es": "spa", "fr": "fra", "de": "deu", "ja": "jpn", "ru": "rus",
                                         "pt": "por", "hi": "hin", "ko": "kor", "it": "ita", "tr": "tur", "id": "ind",
                                         "vi": "vie"}.items()},
    **{k: (v, "rtl") for k, v in {"ar": "ara", "he": "heb", "fa": "fas", "ur": "urd"}.items()},
    **{k: (v, "mid_ltr") for k, v in {"nl": "nld", "pl": "pol", "uk": "ukr", "sv": "swe", "th": "tha", "bn": "ben",
                                       "ta": "tam", "el": "ell", "cs": "ces", "ro": "ron", "hu": "hun", "ms": "msa",
                                       "sw": "swa", "te": "tel", "mr": "mar"}.items()},
    **{k: (v, "long_tail") for k, v in {"ka": "kat", "hy": "hye", "am": "amh", "km": "khm", "my": "mya", "si": "sin",
                                         "ne": "nep", "kk": "kaz", "uz": "uzb", "az": "aze", "lo": "lao", "mn": "mon",
                                         "yo": "yor", "ha": "hau", "zu": "zul", "ps": "pus", "sd": "snd", "ckb": "kur",
                                         "yi": "yid", "dv": "div", "fi": "fin", "da": "dan", "no": "nor", "bg": "bul",
                                         "sr": "srp", "hr": "hrv", "sk": "slk", "lt": "lit", "ml": "mal", "kn": "kan",
                                         "gu": "guj", "pa": "pan"}.items()},
}
ISO3_TO_LANG = {v[0]: k for k, v in LANGS.items()}
RTL_LANGS = {"ar", "he", "fa", "ur", "ps", "sd", "ckb", "yi", "dv", "ug"}
LONG_TAIL_RTL = {"ps", "sd", "ckb", "yi", "dv"}
UNSPACED_LANGS = {"zh", "ja", "th", "lo", "km", "my"}

SCRIPT_RANGES = [
    ("Latn", [(0x41, 0x24F)]), ("Grek", [(0x370, 0x3FF)]), ("Cyrl", [(0x400, 0x52F)]), ("Armn", [(0x530, 0x58F)]),
    ("Hebr", [(0x590, 0x5FF)]), ("Arab", [(0x600, 0x6FF), (0x750, 0x77F), (0xFB50, 0xFDFF), (0xFE70, 0xFEFF)]),
    ("Thaa", [(0x780, 0x7BF)]), ("Deva", [(0x900, 0x97F)]), ("Beng", [(0x980, 0x9FF)]), ("Guru", [(0xA00, 0xA7F)]),
    ("Gujr", [(0xA80, 0xAFF)]), ("Taml", [(0xB80, 0xBFF)]), ("Telu", [(0xC00, 0xC7F)]), ("Knda", [(0xC80, 0xCFF)]),
    ("Mlym", [(0xD00, 0xD7F)]), ("Sinh", [(0xD80, 0xDFF)]), ("Thai", [(0xE00, 0xE7F)]), ("Laoo", [(0xE80, 0xEFF)]),
    ("Mymr", [(0x1000, 0x109F)]), ("Geor", [(0x10A0, 0x10FF)]), ("Hang", [(0x1100, 0x11FF), (0xAC00, 0xD7AF)]),
    ("Ethi", [(0x1200, 0x139F)]), ("Khmr", [(0x1780, 0x17FF)]), ("Mong", [(0x1800, 0x18AF)]),
    ("Kana", [(0x3040, 0x30FF)]), ("Hani", [(0x4E00, 0x9FFF), (0x3400, 0x4DBF)]),
]

REGION_OF = {}
for _region, _codes in {
    "Europe": "AD AL AT BA BE BG BY CH CY CZ DE DK EE ES FI FO FR GB GI GR HR HU IE IM IS IT LI LT LU LV MC MD ME MK MT "
              "NL NO PL PT RO RS RU SE SI SK SM UA VA XK",
    "Asia": "AF AM AZ BD BN BT CN GE HK ID IN JP KG KH KP KR KZ LA LK MM MN MO MV MY NP PH PK SG TH TJ TL TM TW UZ VN",
    "North America": "US CA",
    "MENA": "AE BH DZ EG IL IQ IR JO KW LB LY MA OM PS QA SA SD SY TN TR YE",
    "Latin America": "AG AR BB BO BR BS BZ CL CO CR CU DM DO EC GD GT GY HN HT JM KN LC MX NI PA PE PR PY SR SV TT UY VC VE",
    "Sub-Saharan Africa": "AO BF BI BJ BW CD CF CG CI CM CV DJ ER ET GA GH GM GN GQ GW KE KM LR LS MG ML MR MU MW MZ NA "
                          "NE NG RW SC SL SN SO SS ST SZ TD TG TZ UG ZA ZM ZW",
    "Oceania": "AU FJ KI MH FM NR NZ PG PW SB TO TV VU WS",
}.items():
    for _c in _codes.split():
        REGION_OF[_c] = _region
# ccTLDs that are mostly used as generic vanity domains, so they say nothing about the publisher's country
VANITY_CCTLDS = {"io", "co", "ai", "me", "tv", "ly", "fm", "gg", "to", "ws", "cc", "nu", "sh", "ac", "vc", "la", "so",
                 "eu", "su"}


def db():
    OUT.mkdir(exist_ok=True)
    conn = sqlite3.connect(DB_PATH, timeout=120, check_same_thread=False)
    conn.row_factory = sqlite3.Row
    conn.execute("PRAGMA journal_mode=WAL")
    conn.executescript("""
    CREATE TABLE IF NOT EXISTS candidates (
      url TEXT PRIMARY KEY, domain TEXT, source TEXT, hint_lang TEXT, hint_mime TEXT, hint_bucket TEXT,
      added_at TEXT DEFAULT CURRENT_TIMESTAMP);
    CREATE TABLE IF NOT EXISTS fetches (
      url TEXT PRIMARY KEY, ok INTEGER, reason TEXT, final_url TEXT, http_status INTEGER, fetched_at TEXT, meta TEXT);
    CREATE TABLE IF NOT EXISTS tags (url TEXT PRIMARY KEY, tags TEXT);
    CREATE TABLE IF NOT EXISTS labels (url TEXT PRIMARY KEY, label TEXT, ok INTEGER, reason TEXT);
    CREATE TABLE IF NOT EXISTS verdicts (url TEXT PRIMARY KEY, verdict TEXT, ok INTEGER, reason TEXT);
    CREATE TABLE IF NOT EXISTS stability (url TEXT PRIMARY KEY, ok INTEGER, recall REAL, checked_at TEXT);
    CREATE TABLE IF NOT EXISTS selected (url TEXT PRIMARY KEY, id INTEGER, split TEXT);
    CREATE TABLE IF NOT EXISTS reviews (url TEXT PRIMARY KEY, reviewer TEXT, decision TEXT, truth_text TEXT,
      lie_text TEXT, note TEXT);
    """)
    return conn


def cache_path(url, ext):
    h = hashlib.sha1(url.encode()).hexdigest()
    return CACHE / h[:2] / f"{h}.{ext}"


def snapshot_path(url, ext):
    h = hashlib.sha1(url.encode()).hexdigest()
    return SNAPSHOTS / h[:2] / f"{h}.{ext}"


def write_gz_json(path, obj):
    path.parent.mkdir(parents=True, exist_ok=True)
    with gzip.open(path, "wt", encoding="utf-8") as f:
        json.dump(obj, f, ensure_ascii=False)


def read_gz_json(path):
    with gzip.open(path, "rt", encoding="utf-8") as f:
        return json.load(f)


_tld_extract = None


def registered_domain(url):
    global _tld_extract
    if _tld_extract is None:
        import tldextract
        _tld_extract = tldextract.TLDExtract(suffix_list_urls=())  # bundled snapshot, no network
    ext = _tld_extract(url)
    return (ext.top_domain_under_public_suffix or ext.domain or "").lower(), ext.suffix.lower()


def country_from_suffix(suffix):
    last = (suffix or "").rsplit(".", 1)[-1].lower()
    if len(last) != 2 or last in VANITY_CCTLDS:
        return None
    code = "GB" if last == "uk" else last.upper()
    return code if code in REGION_OF else None


def dominant_script(text):
    counts = {}
    for ch in text[:20000]:
        cp = ord(ch)
        if cp < 0x41:
            continue
        for name, ranges in SCRIPT_RANGES:
            if any(lo <= cp <= hi for lo, hi in ranges):
                counts[name] = counts.get(name, 0) + 1
                break
    if not counts:
        return "Zyyy", counts
    total = sum(counts.values())
    if counts.get("Kana", 0) > 0.05 * total:
        return "Jpan", counts
    best = max(counts, key=counts.get)
    return ("Hani" if best == "Hani" else best), counts


_WORD_RE = re.compile(r"\d+/\d+|[\w'-]+")


def smart_tokenize(text):
    """Same tokenizer as eval.py, so length checks match how rows are scored."""
    return _WORD_RE.findall((text or "").lower())


def normalize(text):
    return re.sub(r"\s+", " ", unicodedata.normalize("NFKC", text or "")).strip()


def copilot(prompt, model, timeout=900, effort=None):
    """Run one non-interactive Copilot call with no tools and return the text reply."""
    cmd = ["copilot", "-s", "--model", model, "--available-tools=", "--disable-builtin-mcps",
           "--no-custom-instructions", "--no-auto-update"]
    if effort:
        cmd += ["--reasoning-effort", effort]
    with tempfile.TemporaryDirectory() as cwd:
        proc = subprocess.run(cmd, input=prompt, capture_output=True, text=True, timeout=timeout, cwd=cwd)
    if proc.returncode != 0:
        raise RuntimeError(f"copilot exit {proc.returncode}: {(proc.stderr or proc.stdout)[-500:]}")
    return proc.stdout


def parse_json_reply(text):
    """Pull the first JSON array/object out of a model reply."""
    text = text.strip()
    fence = re.search(r"```(?:json)?\s*([\s\S]*?)```", text)
    if fence:
        text = fence.group(1)
    starts = [i for i in (text.find("["), text.find("{")) if i >= 0]
    if not starts:
        raise ValueError("no JSON in reply")
    return json.JSONDecoder().raw_decode(text[min(starts):])[0]


def bucket_keys(row):
    """Every quota bucket a dataset row belongs to, as 'dim:value' strings."""
    keys = []
    lang = row.get("language")
    tier = LANGS.get(lang, (None, "other_lang"))[1]
    keys += [f"language:{lang}", f"lang_tier:{tier}", f"direction:{row.get('direction')}",
             f"script:{row.get('script')}", f"render_mode:{row.get('render_mode')}",
             f"platform:{row.get('platform')}", f"source:{row.get('source')}"]
    if lang in LONG_TAIL_RTL:
        keys.append("special:long_tail_rtl")
    for dim in ("page_type", "topic", "region", "country"):
        if row.get(dim):
            keys.append(f"{dim}:{row[dim]}")
    media = [m for m in (row.get("media") or "").split(";") if m]
    difficulty = [d for d in (row.get("difficulty") or "").split(";") if d]
    keys += [f"media:{m}" for m in media]
    keys += [f"difficulty:{d}" for d in difficulty] or ["difficulty:clean"]
    if "pdf" in media:
        if lang != "en":
            keys.append("special:pdf_non_english")
        if lang in RTL_LANGS:
            keys.append("special:pdf_rtl")
    if row.get("page_type"):
        keys.append(f"tier_x_page_type:{tier}|{row['page_type']}")
    if row.get("region"):
        keys.append(f"render_x_region:{row.get('render_mode')}|{row['region']}")
    return keys
