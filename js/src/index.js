/// <reference types="@fastly/js-compute" />

import { Backend } from "fastly:backend";
import { ConfigStore } from "fastly:config-store";

addEventListener("fetch", (event) => event.respondWith(handleRequest(event)));

async function handleRequest(event) {
  const req = event.request;
  const reqUrl = new URL(req.url);

  // Validate API key from config store (with fallback for local development)
  const apiKey = reqUrl.searchParams.get("key");
  let validKey;
  try {
    const config = new ConfigStore("dynserv-key");
    validKey = config.get("key");
  } catch (e) {
    // Fallback for local development when config store is not available
    validKey = "testing";
  }

  if (!apiKey || apiKey !== validKey) {
    return new Response(
      JSON.stringify({
        error: "Unauthorized",
        message: "Invalid or missing API key",
      }),
      {
        status: 403,
        headers: { "Content-Type": "application/json" },
      }
    );
  }

  // Get the target URL from the query parameter
  const targetUrlParam = reqUrl.searchParams.get("url");

  if (!targetUrlParam) {
    return new Response(
      JSON.stringify({
        error: "Missing 'url' query parameter",
        usage: "Add ?url=https://example.com/path to your request",
      }),
      {
        status: 400,
        headers: { "Content-Type": "application/json" },
      }
    );
  }

  let targetUrl;
  try {
    targetUrl = new URL(targetUrlParam);
  } catch (e) {
    return new Response(
      JSON.stringify({
        error: "Invalid URL provided",
        details: e.message,
      }),
      {
        status: 400,
        headers: { "Content-Type": "application/json" },
      }
    );
  }

  // Only allow https protocol (TLS backends only)
  if (targetUrl.protocol !== "https:") {
    return new Response(
      JSON.stringify({
        error: "Only https URLs are supported",
        usage: "Use https:// URLs (e.g., ?url=https://example.com/path)",
      }),
      {
        status: 400,
        headers: { "Content-Type": "application/json" },
      }
    );
  }

  const port = targetUrl.port ? parseInt(targetUrl.port, 10) : 443;
  const hostname = targetUrl.hostname;

  // Create a unique backend name based on host and port
  // Backend names must be alphanumeric with underscores/hyphens
  const backendName = `dyn_${hostname.replace(/[^a-zA-Z0-9]/g, "_")}_${port}`;

  try {
    // Create the dynamic backend with TLS
    const backend = new Backend({
      name: backendName,
      target: `${hostname}:${port}`,
      hostOverride: hostname,
      useSSL: true,
      sniHostname: hostname,
      tlsMinVersion: 1.2,
      tlsMaxVersion: 1.3,
      connectTimeout: 10000,
      firstByteTimeout: 30000,
      betweenBytesTimeout: 30000,
    });

    // Build the request to the origin
    // Preserve the path and query string from the target URL
    const originUrl = targetUrl.pathname + targetUrl.search;

    // Create a new request to the origin
    const originRequest = new Request(originUrl, {
      method: req.method,
      headers: req.headers,
      body: req.body,
    });

    // Override the host header to match the target
    originRequest.headers.set("Host", hostname);

    // Remove headers that shouldn't be forwarded
    originRequest.headers.delete("x-forwarded-for");
    originRequest.headers.delete("x-forwarded-host");
    originRequest.headers.delete("x-forwarded-proto");

    // Fetch from the dynamic backend
    const response = await fetch(originRequest, {
      backend: backend,
      cacheOverride: new CacheOverride("pass"), // Don't cache
    });

    // Return the response from origin
    return response;
  } catch (e) {
    return new Response(
      JSON.stringify({
        error: "Failed to fetch from origin",
        details: e.message,
        target: targetUrlParam,
      }),
      {
        status: 502,
        headers: { "Content-Type": "application/json" },
      }
    );
  }
}
