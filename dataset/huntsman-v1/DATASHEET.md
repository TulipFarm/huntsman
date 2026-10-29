# huntsman-scrape-dataset-v1 datasheet

Generated 2026-10-29. Design and method: SPEC.md.

## Version 1.1

Updated 2026-10-29. Added 320 rows (ids 10001–10320): tulipfarm 320 (32 dev). Their 24 h stability check is pending (SPEC.md §5).

Removed 320 v1 rows (32 dev, 288 test), one per added row in the same split, from the most over-filled buckets. No bucket that v1 met fell below its minimum and no human-reviewed row was removed. Removed rows per bucket:

| bucket | rows removed |
|---|---:|
| language:en | 320 |
| media:html | 281 |
| region:North America | 248 |
| difficulty:clean | 181 |
| render_mode:static | 164 |
| difficulty:cookie_banner | 120 |
| media:image_heavy | 107 |
| render_mode:csr | 93 |
| page_type:homepage_landing | 73 |
| media:video | 72 |
| platform:squarespace | 70 |
| render_mode:ssr | 63 |
| difficulty:anti_bot | 58 |
| platform:wordpress | 54 |
| media:tables | 49 |
| difficulty:lazy_load | 45 |
| topic:technology | 43 |
| page_type:listing_category | 41 |
| page_type:product_page | 39 |
| platform:none | 39 |
| region:Asia | 36 |
| media:long_form | 35 |
| platform:custom | 35 |
| topic:business_finance | 32 |
| page_type:blog_post | 28 |

Removed ids: 4, 33, 43, 99, 213, 256, 304, 311, 312, 330, 336, 348, 350, 409, 476, 538, 578, 603, 613, 696, 721, 724, 798, 821, 854, 893, 958, 1014, 1043, 1055, 1071, 1073, 1118, 1125, 1135, 1139, 1147, 1149, 1164, 1198, 1236, 1247, 1354, 1398, 1442, 1529, 1576, 1581, 1620, 1641, 1651, 1685, 1699, 1711, 1713, 1720, 1730, 1778, 1854, 1871, 1898, 1935, 1997, 2072, 2094, 2100, 2206, 2268, 2280, 2494, 2534, 2543, 2613, 2682, 2683, 2702, 2705, 2707, 2715, 2729, 2752, 2757, 2776, 2782, 2788, 2796, 2801, 2868, 2902, 2903, 2910, 2961, 3059, 3122, 3212, 3235, 3240, 3309, 3446, 3533, 3557, 3596, 3599, 3625, 3673, 3682, 3779, 3902, 3939, 3941, 3943, 3950, 3975, 3997, 3998, 4092, 4151, 4198, 4203, 4259, 4284, 4300, 4304, 4349, 4386, 4438, 4477, 4502, 4514, 4530, 4606, 4640, 4662, 4665, 4727, 4733, 4740, 4753, 4780, 4797, 4799, 4811, 4848, 4923, 4981, 5000, 5018, 5149, 5192, 5194, 5201, 5204, 5253, 5309, 5337, 5339, 5363, 5381, 5386, 5389, 5416, 5456, 5479, 5509, 5524, 5548, 5558, 5605, 5615, 5626, 5628, 5669, 5715, 5730, 5770, 5871, 5873, 5934, 5995, 6041, 6073, 6176, 6181, 6211, 6251, 6264, 6274, 6287, 6307, 6337, 6339, 6359, 6374, 6384, 6390, 6423, 6454, 6464, 6499, 6502, 6534, 6540, 6548, 6552, 6617, 6640, 6641, 6655, 6726, 6754, 6778, 6836, 6870, 6876, 6882, 6890, 6925, 6934, 6938, 6977, 7027, 7055, 7072, 7087, 7094, 7197, 7208, 7269, 7337, 7382, 7512, 7555, 7556, 7561, 7712, 7726, 7743, 7754, 7760, 7795, 7803, 7813, 7872, 7873, 7889, 7915, 7917, 7983, 7990, 8013, 8023, 8027, 8111, 8115, 8217, 8221, 8277, 8308, 8325, 8344, 8355, 8373, 8392, 8438, 8468, 8478, 8499, 8501, 8530, 8575, 8614, 8635, 8657, 8658, 8705, 8724, 8756, 8780, 8790, 8872, 8880, 8891, 8914, 8918, 8963, 8964, 8999, 9008, 9012, 9036, 9053, 9163, 9168, 9393, 9405, 9434, 9454, 9455, 9510, 9516, 9551, 9577, 9633, 9638, 9674, 9681, 9684, 9694, 9696, 9703, 9719, 9742, 9749, 9753, 9770, 9778, 9836, 9850, 9853, 9945.

Columns: `id`, `url`, `truth_text`, `lie_text`, `error`, `split`, `language`, `lang_tier`, `script`, `direction`, `region`, `country`, `render_mode`, `platform`, `page_type`, `topic`, `media`, `difficulty`, `source`, `review`, `domain`, `final_url`, `http_status`, `fetched_at`. `media` and `difficulty` are `;`-separated (empty difficulty = clean).

10000 rows, 8763 domains, 226 countries, 63 languages.

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
| ltr | 8599 | 86.0% |
| rtl | 1233 | 12.3% |
| mixed | 168 | 1.7% |

## script

| value | rows | share |
|---|---:|---:|
| Latn | 6002 | 60.0% |
| Arab | 1028 | 10.3% |
| Cyrl | 518 | 5.2% |
| Deva | 430 | 4.3% |
| Hebr | 277 | 2.8% |
| Jpan | 273 | 2.7% |
| Hang | 256 | 2.6% |
| Hani | 232 | 2.3% |
| Taml | 117 | 1.2% |
| Beng | 116 | 1.2% |
| Grek | 112 | 1.1% |
| Thai | 112 | 1.1% |
| Telu | 112 | 1.1% |
| Thaa | 36 | 0.4% |
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
| Asia | 3423 | 34.2% |
| Europe | 2388 | 23.9% |
| North America | 1377 | 13.8% |
| MENA | 1157 | 11.6% |
| Latin America | 673 | 6.7% |
| Sub-Saharan Africa | 508 | 5.1% |
| unknown | 279 | 2.8% |
| Oceania | 195 | 1.9% |

## render_mode

| value | rows | share |
|---|---:|---:|
| static | 5496 | 55.0% |
| ssr | 2961 | 29.6% |
| csr | 1543 | 15.4% |

## platform

| value | rows | share |
|---|---:|---:|
| custom | 2812 | 28.1% |
| wordpress | 2135 | 21.3% |
| nextjs | 1360 | 13.6% |
| none | 856 | 8.6% |
| react_vue_spa | 827 | 8.3% |
| wix | 362 | 3.6% |
| gatsby_astro | 258 | 2.6% |
| nuxt | 188 | 1.9% |
| drupal | 169 | 1.7% |
| webflow | 156 | 1.6% |
| shopify | 156 | 1.6% |
| squarespace | 156 | 1.6% |
| angular | 149 | 1.5% |
| docs_generator | 120 | 1.2% |
| mediawiki | 102 | 1.0% |
| forum_software | 100 | 1.0% |
| ghost | 72 | 0.7% |
| amp | 22 | 0.2% |

## page_type

| value | rows | share |
|---|---:|---:|
| blog_post | 1473 | 14.7% |
| listing_category | 1243 | 12.4% |
| product_page | 1148 | 11.5% |
| news_article | 1110 | 11.1% |
| homepage_landing | 1067 | 10.7% |
| docs_tutorial | 864 | 8.6% |
| company_info | 521 | 5.2% |
| government_org | 475 | 4.8% |
| legal_policy | 326 | 3.3% |
| reference_wiki | 312 | 3.1% |
| other | 281 | 2.8% |
| profile_directory | 207 | 2.1% |
| event | 206 | 2.1% |
| forum_qa | 206 | 2.1% |
| academic_research | 206 | 2.1% |
| job_listing | 200 | 2.0% |
| media | 153 | 1.5% |
| login_error | 2 | 0.0% |

## topic

| value | rows | share |
|---|---:|---:|
| technology | 1597 | 16.0% |
| business_finance | 1152 | 11.5% |
| shopping_retail | 757 | 7.6% |
| education | 709 | 7.1% |
| news_current_events | 682 | 6.8% |
| government_politics | 618 | 6.2% |
| health | 583 | 5.8% |
| arts_culture | 572 | 5.7% |
| travel | 503 | 5.0% |
| law | 426 | 4.3% |
| entertainment | 337 | 3.4% |
| careers | 330 | 3.3% |
| food_drink | 327 | 3.3% |
| sports | 310 | 3.1% |
| religion | 307 | 3.1% |
| science | 273 | 2.7% |
| real_estate | 271 | 2.7% |
| automotive | 246 | 2.5% |

## source

| value | rows | share |
|---|---:|---:|
| commoncrawl | 5959 | 59.6% |
| search | 3221 | 32.2% |
| tranco | 500 | 5.0% |
| tulipfarm | 320 | 3.2% |

## review

| value | rows | share |
|---|---:|---:|
| none | 7443 | 74.4% |
| llm_review | 2557 | 25.6% |

## media

| value | rows | share |
|---|---:|---:|
| html | 9144 | 91.4% |
| image_heavy | 2065 | 20.6% |
| video | 938 | 9.4% |
| tables | 774 | 7.7% |
| pdf | 635 | 6.3% |
| long_form | 619 | 6.2% |
| code | 579 | 5.8% |
| office | 221 | 2.2% |
| audio | 210 | 2.1% |
| iframe_content | 197 | 2.0% |
| math | 77 | 0.8% |

## difficulty

| value | rows | share |
|---|---:|---:|
| clean | 6996 | 70.0% |
| cookie_banner | 1729 | 17.3% |
| lazy_load | 626 | 6.3% |
| anti_bot | 406 | 4.1% |
| non_utf8 | 284 | 2.8% |
| paywall | 245 | 2.5% |
| shadow_dom | 161 | 1.6% |
| geo_redirect | 132 | 1.3% |
| huge_dom | 88 | 0.9% |
| login_wall | 47 | 0.5% |
| redirect_chain | 23 | 0.2% |
