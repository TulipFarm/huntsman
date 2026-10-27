//! Firecrawl-shaped `/v1/scrape` endpoint over spider-rs, following the scrape path of spider_mcp.
use axum::{extract::State, http::StatusCode, routing::{get, post}, Json, Router};
use serde::Deserialize;
use serde_json::{json, Value};
use spider::configuration::WaitForIdleNetwork;
use spider::features::chrome_common::RequestInterceptConfiguration;
use spider::website::Website;
use spider_transformations::transformation::content::{
    transform_content_input, ReturnFormat, TransformConfig, TransformInput,
};
use std::{sync::Arc, time::Duration};
use tokio::sync::Semaphore;

#[derive(Deserialize)]
struct ScrapeRequest {
    url: String,
    timeout: Option<u64>,
    #[serde(default)]
    formats: Vec<String>,
}

async fn render(url: String, timeout: Duration) -> Result<(u16, String, String), String> {
    let mut website = Website::new(&url);
    website
        .with_limit(1)
        .with_respect_robots_txt(false)
        .with_request_timeout(Some(timeout))
        .with_block_assets(false)
        .with_modify_headers(false)
        .with_chrome_intercept(RequestInterceptConfiguration::new(true))
        .with_wait_for_idle_network(Some(WaitForIdleNetwork::new(Some(Duration::from_millis(500)))));
    let mut website = website.build().map_err(|_| "invalid URL".to_string())?;
    let mut rx = website.subscribe(16);
    website.crawl().await;
    let page = rx.try_recv().map_err(|_| "no page returned".to_string())?;
    let config = TransformConfig { return_format: ReturnFormat::Markdown, ..Default::default() };
    let input = TransformInput {
        url: page.get_url_parsed_ref().as_ref(),
        content: page.get_html_bytes_u8(),
        screenshot_bytes: None,
        encoding: None,
        selector_config: None,
        ignore_tags: None,
    };
    let html = page.get_html();
    Ok((page.status_code.as_u16(), transform_content_input(input, &config), html))
}

async fn scrape(State(slots): State<Arc<Semaphore>>, Json(req): Json<ScrapeRequest>) -> (StatusCode, Json<Value>) {
    let _slot = slots.acquire().await.expect("semaphore closed");
    let timeout = Duration::from_millis(req.timeout.unwrap_or(30_000));
    match tokio::time::timeout(timeout, render(req.url, timeout)).await {
        Ok(Ok((code, markdown, html))) => {
            let mut data = json!({"markdown": markdown, "metadata": {"statusCode": code}});
            if req.formats.iter().any(|f| f == "rawHtml") {
                data["rawHtml"] = json!(html);
            }
            (StatusCode::OK, Json(json!({"success": true, "data": data})))
        }
        Ok(Err(error)) => (StatusCode::BAD_GATEWAY, Json(json!({"success": false, "error": error}))),
        Err(_) => (StatusCode::REQUEST_TIMEOUT, Json(json!({"success": false, "error": "timed out"}))),
    }
}

#[tokio::main]
async fn main() {
    env_logger::init();
    let concurrency = std::env::var("SPIDER_CONCURRENCY").ok().and_then(|v| v.parse().ok()).unwrap_or(8);
    let app = Router::new()
        .route("/health", get(|| async { "ok" }))
        .route("/v1/scrape", post(scrape))
        .with_state(Arc::new(Semaphore::new(concurrency)));
    let listener = tokio::net::TcpListener::bind("0.0.0.0:8080").await.expect("bind");
    axum::serve(listener, app)
        .with_graceful_shutdown(async { let _ = tokio::signal::ctrl_c().await; })
        .await
        .expect("serve");
}
