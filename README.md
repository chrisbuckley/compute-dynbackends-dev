# compute-dynbackends-dev

A Fastly Compute@Edge service that uses Dynamic Backends to proxy requests to arbitrary HTTPS origins. The service accepts a target URL via query parameter and forwards requests to that origin, creating dynamic backend connections at runtime.

Implemented in three languages: **JavaScript**, **Rust**, and **Go**.

## Prerequisites

- [Fastly CLI](https://www.fastly.com/documentation/reference/tools/cli/) installed and authenticated (`fastly profile create`)
- A Fastly Compute service with [Dynamic Backends enabled](https://www.fastly.com/documentation/guides/compute/concepts/dynamic-backends/)

### Language-specific requirements

| Language | Requirements |
|----------|--------------|
| JavaScript | Node.js 18+ |
| Rust | Rust toolchain with `wasm32-wasip1` target |
| Go | [TinyGo](https://tinygo.org/getting-started/install/) |

## Installation

```bash
git clone https://github.com/chrisbuckley/compute-dynbackends-dev.git
cd compute-dynbackends-dev
```

## Build & Run Locally

Choose your preferred language implementation:

### JavaScript
```bash
cd js
npm install
fastly compute serve
```

### Rust
```bash
cd rust
fastly compute serve
```

### Go
```bash
cd go
go mod tidy
fastly compute serve
```

## Deploy to Fastly

From any implementation directory:
```bash
fastly compute publish
```

## Usage

Once running (locally on port 7676, or deployed to your Fastly domain):

```bash
curl "http://localhost:7676/?key=testing&url=https://httpbin.org/get"
```

### Parameters

| Parameter | Required | Description |
|-----------|----------|-------------|
| `key` | Yes | API key (currently hardcoded as `testing`) |
| `url` | Yes | Target HTTPS URL to proxy to |

### Example Requests

```bash
# GET request
curl "http://localhost:7676/?key=testing&url=https://httpbin.org/get"

# POST request with body
curl -X POST "http://localhost:7676/?key=testing&url=https://httpbin.org/post" \
  -H "Content-Type: application/json" \
  -d '{"hello": "world"}'

# With custom headers
curl "http://localhost:7676/?key=testing&url=https://httpbin.org/headers" \
  -H "X-Custom-Header: test"
```

## Limitations

- Only HTTPS URLs are supported (TLS backends only)
- Responses are not cached (pass-through only)

## License

MIT
