# huntsman-scrape-dataset-v1 datasheet

Generated 2026-10-27. Design and method: SPEC.md.

Columns: `id`, `url`, `truth_text`, `lie_text`, `error`, `split`, `language`, `lang_tier`, `script`, `direction`, `region`, `country`, `render_mode`, `platform`, `page_type`, `topic`, `media`, `difficulty`, `source`, `review`, `domain`, `final_url`, `http_status`, `fetched_at`. `media` and `difficulty` are `;`-separated (empty difficulty = clean).

10000 rows, 9036 domains, 226 countries, 63 languages.

## split

| value | rows | share |
|---|---:|---:|
| test | 9000 | 90.0% |
| dev | 1000 | 10.0% |

## lang_tier

| value | rows | share |
|---|---:|---:|
| major_ltr | 3913 | 39.1% |
| english | 2001 | 20.0% |
| mid_ltr | 1813 | 18.1% |
| rtl | 1204 | 12.0% |
| long_tail | 1069 | 10.7% |

## language

| value | rows | share |
|---|---:|---:|
| en | 2001 | 20.0% |
| ar | 501 | 5.0% |
| id | 301 | 3.0% |
| vi | 301 | 3.0% |
| pt | 301 | 3.0% |
| it | 301 | 3.0% |
| ja | 301 | 3.0% |
| es | 301 | 3.0% |
| ru | 301 | 3.0% |
| ko | 301 | 3.0% |
| hi | 301 | 3.0% |
| fr | 301 | 3.0% |
| de | 301 | 3.0% |
| zh | 301 | 3.0% |
| tr | 301 | 3.0% |
| he | 251 | 2.5% |
| fa | 251 | 2.5% |
| ur | 201 | 2.0% |
| hu | 121 | 1.2% |
| sw | 121 | 1.2% |
| el | 121 | 1.2% |
| cs | 121 | 1.2% |
| pl | 121 | 1.2% |
| te | 121 | 1.2% |
| th | 121 | 1.2% |
| nl | 121 | 1.2% |
| mr | 121 | 1.2% |
| sv | 121 | 1.2% |
| ro | 121 | 1.2% |
| ta | 121 | 1.2% |
| bn | 121 | 1.2% |
| uk | 121 | 1.2% |
| ms | 119 | 1.2% |
| hr | 36 | 0.4% |
| pa | 36 | 0.4% |
| am | 36 | 0.4% |
| no | 36 | 0.4% |
| dv | 36 | 0.4% |
| kk | 36 | 0.4% |
| lo | 36 | 0.4% |
| km | 36 | 0.4% |
| bg | 36 | 0.4% |
| mn | 36 | 0.4% |
| ckb | 36 | 0.4% |
| kn | 36 | 0.4% |
| az | 36 | 0.4% |
| ml | 36 | 0.4% |
| sk | 36 | 0.4% |
| lt | 36 | 0.4% |
| gu | 36 | 0.4% |
| ka | 36 | 0.4% |
| uz | 36 | 0.4% |
| sr | 36 | 0.4% |
| fi | 36 | 0.4% |
| hy | 36 | 0.4% |
| ps | 36 | 0.4% |
| ne | 36 | 0.4% |
| si | 36 | 0.4% |
| yi | 35 | 0.4% |
| my | 35 | 0.4% |
| sd | 35 | 0.4% |
| da | 35 | 0.4% |
| yo | 29 | 0.3% |

## direction

| value | rows | share |
|---|---:|---:|
| ltr | 8588 | 85.9% |
| rtl | 1242 | 12.4% |
| mixed | 170 | 1.7% |

## script

| value | rows | share |
|---|---:|---:|
| Latn | 5989 | 59.9% |
| Arab | 1036 | 10.4% |
| Cyrl | 519 | 5.2% |
| Deva | 430 | 4.3% |
| Hebr | 279 | 2.8% |
| Jpan | 273 | 2.7% |
| Hang | 257 | 2.6% |
| Hani | 232 | 2.3% |
| Taml | 117 | 1.2% |
| Beng | 116 | 1.2% |
| Grek | 112 | 1.1% |
| Thai | 112 | 1.1% |
| Telu | 112 | 1.1% |
| Thaa | 37 | 0.4% |
| Laoo | 36 | 0.4% |
| Khmr | 36 | 0.4% |
| Gujr | 36 | 0.4% |
| Geor | 36 | 0.4% |
| Mymr | 35 | 0.4% |
| Guru | 34 | 0.3% |
| Ethi | 34 | 0.3% |
| Knda | 34 | 0.3% |
| Mlym | 34 | 0.3% |
| Sinh | 33 | 0.3% |
| Armn | 31 | 0.3% |

## region

| value | rows | share |
|---|---:|---:|
| Asia | 3459 | 34.6% |
| Europe | 2415 | 24.1% |
| North America | 1305 | 13.1% |
| MENA | 1165 | 11.7% |
| Latin America | 673 | 6.7% |
| Sub-Saharan Africa | 508 | 5.1% |
| unknown | 280 | 2.8% |
| Oceania | 195 | 1.9% |

## render_mode

| value | rows | share |
|---|---:|---:|
| static | 5659 | 56.6% |
| ssr | 2705 | 27.1% |
| csr | 1636 | 16.4% |

## platform

| value | rows | share |
|---|---:|---:|
| custom | 2846 | 28.5% |
| wordpress | 2189 | 21.9% |
| nextjs | 1132 | 11.3% |
| none | 895 | 8.9% |
| react_vue_spa | 853 | 8.5% |
| wix | 378 | 3.8% |
| squarespace | 226 | 2.3% |
| gatsby_astro | 198 | 2.0% |
| nuxt | 191 | 1.9% |
| drupal | 179 | 1.8% |
| webflow | 178 | 1.8% |
| shopify | 170 | 1.7% |
| angular | 149 | 1.5% |
| docs_generator | 120 | 1.2% |
| mediawiki | 102 | 1.0% |
| forum_software | 100 | 1.0% |
| ghost | 72 | 0.7% |
| amp | 22 | 0.2% |

## page_type

| value | rows | share |
|---|---:|---:|
| blog_post | 1500 | 15.0% |
| listing_category | 1281 | 12.8% |
| homepage_landing | 1138 | 11.4% |
| news_article | 1126 | 11.3% |
| product_page | 944 | 9.4% |
| docs_tutorial | 816 | 8.2% |
| company_info | 544 | 5.4% |
| government_org | 479 | 4.8% |
| legal_policy | 330 | 3.3% |
| reference_wiki | 324 | 3.2% |
| other | 300 | 3.0% |
| event | 222 | 2.2% |
| profile_directory | 219 | 2.2% |
| academic_research | 212 | 2.1% |
| forum_qa | 210 | 2.1% |
| job_listing | 200 | 2.0% |
| media | 153 | 1.5% |
| login_error | 2 | 0.0% |

## topic

| value | rows | share |
|---|---:|---:|
| technology | 1335 | 13.4% |
| business_finance | 1171 | 11.7% |
| shopping_retail | 778 | 7.8% |
| education | 724 | 7.2% |
| news_current_events | 690 | 6.9% |
| government_politics | 624 | 6.2% |
| health | 610 | 6.1% |
| arts_culture | 592 | 5.9% |
| travel | 528 | 5.3% |
| law | 432 | 4.3% |
| entertainment | 355 | 3.5% |
| food_drink | 351 | 3.5% |
| careers | 334 | 3.3% |
| sports | 331 | 3.3% |
| religion | 319 | 3.2% |
| real_estate | 286 | 2.9% |
| science | 282 | 2.8% |
| automotive | 258 | 2.6% |

## source

| value | rows | share |
|---|---:|---:|
| commoncrawl | 6122 | 61.2% |
| search | 3358 | 33.6% |
| tranco | 520 | 5.2% |

## review

| value | rows | share |
|---|---:|---:|
| none | 7423 | 74.2% |
| llm_review | 2577 | 25.8% |

## media

| value | rows | share |
|---|---:|---:|
| html | 9105 | 91.0% |
| image_heavy | 2172 | 21.7% |
| video | 1010 | 10.1% |
| tables | 791 | 7.9% |
| pdf | 657 | 6.6% |
| long_form | 654 | 6.5% |
| code | 574 | 5.7% |
| office | 238 | 2.4% |
| audio | 227 | 2.3% |
| iframe_content | 213 | 2.1% |
| math | 77 | 0.8% |

## difficulty

| value | rows | share |
|---|---:|---:|
| clean | 6859 | 68.6% |
| cookie_banner | 1848 | 18.5% |
| lazy_load | 670 | 6.7% |
| anti_bot | 464 | 4.6% |
| non_utf8 | 292 | 2.9% |
| paywall | 245 | 2.5% |
| shadow_dom | 161 | 1.6% |
| geo_redirect | 132 | 1.3% |
| huge_dom | 88 | 0.9% |
| login_wall | 47 | 0.5% |
| redirect_chain | 23 | 0.2% |
