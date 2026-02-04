use fastly::http::StatusCode;
use fastly::{backend::BackendBuilder, Error, Request, Response};
use std::time::Duration;
use url::Url;

/// SSRF Protection: Check if hostname is a private/internal address
fn is_private_host(hostname: &str) -> bool {
    let lower_host = hostname.to_lowercase();

    // Block localhost variants
    if lower_host == "localhost" || lower_host == "localhost.localdomain" {
        return true;
    }

    // Block IPv6 localhost
    if lower_host == "::1" || lower_host == "[::1]" {
        return true;
    }

    // Check for IPv4 address patterns
    let parts: Vec<&str> = hostname.split('.').collect();
    if parts.len() == 4 {
        let octets: Result<Vec<u8>, _> = parts.iter().map(|p| p.parse::<u8>()).collect();
        if let Ok(octets) = octets {
            let (a, b, _, _) = (octets[0], octets[1], octets[2], octets[3]);

            // Loopback: 127.0.0.0/8
            if a == 127 {
                return true;
            }

            // Private: 10.0.0.0/8
            if a == 10 {
                return true;
            }

            // Private: 172.16.0.0/12
            if a == 172 && (16..=31).contains(&b) {
                return true;
            }

            // Private: 192.168.0.0/16
            if a == 192 && b == 168 {
                return true;
            }

            // Link-local: 169.254.0.0/16 (includes AWS metadata endpoint)
            if a == 169 && b == 254 {
                return true;
            }

            // Current network: 0.0.0.0/8
            if a == 0 {
                return true;
            }
        }
    }

    // Block common internal hostnames
    let internal_patterns = [
        "internal.",
        "intranet.",
        "private.",
        "corp.",
        "lan.",
    ];
    for pattern in internal_patterns {
        if lower_host.starts_with(pattern) {
            return true;
        }
    }

    let internal_suffixes = [".internal", ".local", ".localhost"];
    for suffix in internal_suffixes {
        if lower_host.ends_with(suffix) {
            return true;
        }
    }

    false
}

#[fastly::main]
fn main(mut req: Request) -> Result<Response, Error> {
    let req_url = req.get_url().clone();

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

    // SSRF Protection: Block requests to private/internal hosts
    if is_private_host(&hostname) {
        return Ok(Response::from_status(StatusCode::FORBIDDEN)
            .with_header("Content-Type", "application/json")
            .with_body(
                r#"{"error":"Forbidden","message":"Requests to private or internal hosts are not allowed"}"#,
            ));
    }

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

    // Build the origin URL path with query string
    let origin_path = match target_url.query() {
        Some(q) => format!("{}?{}", target_url.path(), q),
        None => target_url.path().to_string(),
    };

    // Modify the request URL to the target
    req.set_url(target_url.clone());
    req.set_path(&origin_path);

    // Remove headers that shouldn't be forwarded
    req.remove_header("x-forwarded-for");
    req.remove_header("x-forwarded-host");
    req.remove_header("x-forwarded-proto");

    // Set the host header to match the target
    req.set_header("Host", &hostname);

    // Set pass to bypass cache
    req.set_pass(true);

    // Fetch from the dynamic backend
    match req.send(backend.name()) {
        Ok(response) => Ok(response),
        Err(e) => Ok(Response::from_status(StatusCode::BAD_GATEWAY)
            .with_header("Content-Type", "application/json")
            .with_body(format!(
                r#"{{"error":"Failed to fetch from origin","details":"{}","target":"{}"}}"#,
                e, target_url_str
            ))),
    }
}
