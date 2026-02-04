# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a Fastly Compute@Edge service that uses Dynamic Backends to proxy requests to arbitrary HTTPS origins. The service accepts a URL via query parameter and forwards requests to that target, creating dynamic backend connections at runtime.

## Build and Deploy Commands

```bash
# Build the Wasm binary
npm run build

# Deploy to Fastly
npm run deploy
# or: fastly compute publish

# Run locally for testing
fastly compute serve
```

## Architecture

**Single-file service** (`src/index.js`): A fetch event handler that:
1. Validates API key from `?key=` query parameter
2. Extracts target URL from `?url=` query parameter
3. Creates a dynamic TLS backend for the target host
4. Proxies the request and returns the response

**Key APIs used:**
- `Backend` from `fastly:backend` - Creates dynamic backends at runtime
- `CacheOverride` - Bypasses caching for proxied requests

**Request flow:**
```
Client -> Fastly Edge -> Dynamic Backend -> Origin
         (validates)    (creates TLS conn)
```

## Testing

Test locally with `fastly compute serve`, then use curl:
```bash
curl "http://localhost:7676/?key=testing&url=https://httpbin.org/get"
```

## Important Constraints

- Only HTTPS URLs are supported (TLS backends only)
- Backend names are generated as `dyn_{hostname}_{port}` with sanitized hostnames
- Requests bypass Fastly's cache (`CacheOverride("pass")`)
