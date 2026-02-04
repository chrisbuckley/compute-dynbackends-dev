# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a Fastly Compute@Edge service that uses Dynamic Backends to proxy requests to arbitrary HTTPS origins. The service accepts a URL via query parameter and forwards requests to that target, creating dynamic backend connections at runtime.

The same functionality is implemented in three languages: JavaScript, Rust, and Go.

## Build and Deploy Commands

Each implementation lives in its own directory with its own `fastly.toml`:

### JavaScript (`js/`)
```bash
cd js
npm install
npm run build                  # Build the Wasm binary
fastly compute serve           # Run locally
fastly compute publish         # Deploy to Fastly
```

### Rust (`rust/`)
```bash
cd rust
fastly compute serve           # Run locally (builds automatically)
fastly compute publish         # Deploy to Fastly
```

### Go (`go/`)
Requires [TinyGo](https://tinygo.org/getting-started/install/) installed.
```bash
cd go
go mod tidy                    # Download dependencies
fastly compute serve           # Run locally (builds automatically)
fastly compute publish         # Deploy to Fastly
```

## Architecture

Each implementation is a single-file service that:
1. Validates API key from `?key=` query parameter
2. Extracts target URL from `?url=` query parameter
3. Creates a dynamic TLS backend for the target host
4. Proxies the request and returns the response

**Request flow:**
```
Client -> Fastly Edge -> Dynamic Backend -> Origin
         (validates)    (creates TLS conn)
```

| Language | Entry Point | Key APIs |
|----------|-------------|----------|
| JS | `js/src/index.js` | `Backend` from `fastly:backend`, `CacheOverride` |
| Rust | `rust/src/main.rs` | `BackendBuilder`, `CacheOverride::Pass` |
| Go | `go/main.go` | `fsthttp.RegisterDynamicBackend`, `CacheOptions.Pass` |

## Testing

Test locally with `fastly compute serve` (from any implementation directory), then:
```bash
curl "http://localhost:7676/?key=testing&url=https://httpbin.org/get"
```

## Important Constraints

- Only HTTPS URLs are supported (TLS backends only)
- Backend names are generated as `dyn_{hostname}_{port}` with sanitized hostnames
- Requests bypass Fastly's cache
