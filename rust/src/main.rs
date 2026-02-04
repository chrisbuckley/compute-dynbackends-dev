use fastly::http::{Method, StatusCode};
use fastly::{backend::BackendBuilder, cache_override::CacheOverride, Error, Request, Response};
use std::time::Duration;
use url::Url;

#[fastly::main]
fn main(req: Request) -> Result<Response, Error> {
    let req_url = req.get_url();

    // Validate API key
    let api_key = req_url.query_pairs().find(|(k, _)| k == "key").map(|(_, v)| v);
    match api_key {
        Some(key) if key == "testing" => {}
        _ => {
            return Ok(Response::from_status(StatusCode::FORBIDDEN)
                .with_header("Content-Type", "application/json")
                .with_body(r#"{"error":"Unauthorized","message":"Invalid or missing API key"}"#));
        }
    }

    // Get the target URL from the query parameter
    let target_url_param = req_url.query_pairs().find(|(k, _)| k == "url").map(|(_, v)| v);
    let target_url_str = match target_url_param {
        Some(url) => url.to_string(),
        None => {
            return Ok(Response::from_status(StatusCode::BAD_REQUEST)
                .with_header("Content-Type", "application/json")
                .with_body(
                    r#"{"error":"Missing 'url' query parameter","usage":"Add ?url=https://example.com/path to your request"}"#,
                ));
        }
    };

    // Parse the target URL
    let target_url = match Url::parse(&target_url_str) {
        Ok(url) => url,
        Err(e) => {
            return Ok(Response::from_status(StatusCode::BAD_REQUEST)
                .with_header("Content-Type", "application/json")
                .with_body(format!(
                    r#"{{"error":"Invalid URL provided","details":"{}"}}"#,
                    e
                )));
        }
    };

    // Only allow https protocol (TLS backends only)
    if target_url.scheme() != "https" {
        return Ok(Response::from_status(StatusCode::BAD_REQUEST)
            .with_header("Content-Type", "application/json")
            .with_body(
                r#"{"error":"Only https URLs are supported","usage":"Use https:// URLs (e.g., ?url=https://example.com/path)"}"#,
            ));
    }

    let hostname = match target_url.host_str() {
        Some(h) => h.to_string(),
        None => {
            return Ok(Response::from_status(StatusCode::BAD_REQUEST)
                .with_header("Content-Type", "application/json")
                .with_body(r#"{"error":"Invalid URL: missing hostname"}"#));
        }
    };

    let port = target_url.port().unwrap_or(443);

    // Create a unique backend name based on host and port
    // Backend names must be alphanumeric with underscores/hyphens
    let sanitized_hostname: String = hostname
        .chars()
        .map(|c| if c.is_alphanumeric() { c } else { '_' })
        .collect();
    let backend_name = format!("dyn_{}_{}", sanitized_hostname, port);

    // Create the dynamic backend with TLS
    let backend = match BackendBuilder::new(&backend_name, format!("{}:{}", hostname, port))
        .override_host(&hostname)
        .enable_ssl()
        .sni_hostname(&hostname)
        .check_certificate(&hostname)
        .connect_timeout(Duration::from_secs(10))
        .first_byte_timeout(Duration::from_secs(30))
        .between_bytes_timeout(Duration::from_secs(30))
        .finish()
    {
        Ok(b) => b,
        Err(e) => {
            return Ok(Response::from_status(StatusCode::BAD_GATEWAY)
                .with_header("Content-Type", "application/json")
                .with_body(format!(
                    r#"{{"error":"Failed to create backend","details":"{:?}","target":"{}"}}"#,
                    e, target_url_str
                )));
        }
    };

    // Build the request to the origin
    // Preserve the path and query string from the target URL
    let origin_path = match target_url.query() {
        Some(q) => format!("{}?{}", target_url.path(), q),
        None => target_url.path().to_string(),
    };

    // Create a new request to the origin
    let mut origin_request = Request::new(req.get_method().clone(), &origin_path);

    // Copy headers from original request
    for (name, value) in req.get_headers() {
        // Skip headers that shouldn't be forwarded
        let name_str = name.as_str().to_lowercase();
        if name_str == "x-forwarded-for"
            || name_str == "x-forwarded-host"
            || name_str == "x-forwarded-proto"
            || name_str == "host"
        {
            continue;
        }
        origin_request.set_header(name, value);
    }

    // Set the host header to match the target
    origin_request.set_header("Host", &hostname);

    // Copy body for methods that typically have one
    if req.get_method() == Method::POST
        || req.get_method() == Method::PUT
        || req.get_method() == Method::PATCH
    {
        origin_request.set_body(req.into_body());
    }

    // Set cache override to pass (don't cache)
    origin_request.set_cache_override(CacheOverride::Pass);

    // Fetch from the dynamic backend
    match origin_request.send(backend.name()) {
        Ok(response) => Ok(response),
        Err(e) => Ok(Response::from_status(StatusCode::BAD_GATEWAY)
            .with_header("Content-Type", "application/json")
            .with_body(format!(
                r#"{{"error":"Failed to fetch from origin","details":"{}","target":"{}"}}"#,
                e, target_url_str
            ))),
    }
}
