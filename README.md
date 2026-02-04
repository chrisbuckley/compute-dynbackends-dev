# compute-dynbackends-dev

A Fastly Compute@Edge service that uses Dynamic Backends to proxy requests to arbitrary HTTPS origins. The service accepts a target URL via query parameter and forwards requests to that origin, creating dynamic backend connections at runtime.

Implemented in three languages: **JavaScript**, **Rust**, and **Go**.

## Prerequisites

- [Fastly CLI](https://www.fastly.com/documentation/reference/tools/cli/) installed and authenticated (`fastly profile create`)
- A Fastly Compute service with [Dynamic Backends enabled](https://www.fastly.com/documentation/guides/compute/concepts/dynamic-backends/)
- A Fastly Config Store named `dynserv-key` containing the API key (see [Config Store Setup](#config-store-setup))

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

## Config Store Setup

The API key is stored in a Fastly Config Store named `dynserv-key`. For production deployments, you must create this config store and link it to your service.

### Local Development

For local development, the service falls back to the default key `testing` when the config store is not available. No additional setup is required.

### Production Setup

1. **Create the config store:**
```bash
fastly config-store create --name dynserv-key
```

2. **Get the store ID:**
```bash
fastly config-store list
```

3. **Add the API key** (replace `your-secret-key` with your actual key):
```bash
fastly config-store-entry create --store-id <STORE_ID> --key key --value your-secret-key
```

4. **Link to your service** (after deploying):
```bash
fastly resource-link create --version latest --resource-id <STORE_ID> --autoclone
fastly service-version activate --version latest
```

## Deploy to Fastly

From any implementation directory:
```bash
fastly compute publish
```

After deploying, remember to [link the config store](#link-to-your-service) to your service.

## Usage

Once running (locally on port 7676, or deployed to your Fastly domain):

```bash
curl "http://localhost:7676/?key=testing&url=https://httpbin.org/get"
```

### Parameters

| Parameter | Required | Description |
|-----------|----------|-------------|
| `key` | Yes | API key (must match value in `dynserv-key` config store) |
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
