/// <reference types="@fastly/js-compute" />

import { Backend } from "fastly:backend";
import { ConfigStore } from "fastly:config-store";

// SSRF Protection: Check if hostname is a private/internal address
function isPrivateHost(hostname) {
  const lowerHost = hostname.toLowerCase();

  // Block localhost variants
  if (lowerHost === "localhost" || lowerHost === "localhost.localdomain") {
    return true;
  }

  // Check for IP address patterns
  const ipv4Regex = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/;
  const match = hostname.match(ipv4Regex);

  if (match) {
    const [, a, b, c, d] = match.map(Number);

    // Validate octets
    if (a > 255 || b > 255 || c > 255 || d > 255) {
      return true; // Invalid IP, block it
    }

    // Loopback: 127.0.0.0/8
    if (a === 127) return true;

    // Private: 10.0.0.0/8
    if (a === 10) return true;

    // Private: 172.16.0.0/12
    if (a === 172 && b >= 16 && b <= 31) return true;

    // Private: 192.168.0.0/16
    if (a === 192 && b === 168) return true;

    // Link-local: 169.254.0.0/16 (includes AWS metadata endpoint)
    if (a === 169 && b === 254) return true;

    // Broadcast
    if (a === 255 && b === 255 && c === 255 && d === 255) return true;

    // Current network: 0.0.0.0/8
    if (a === 0) return true;
  }

  // Block IPv6 localhost
  if (lowerHost === "::1" || lowerHost === "[::1]") {
    return true;
  }

  // Block common internal hostnames
  const internalPatterns = [
    /^internal\./i,
    /^intranet\./i,
    /^private\./i,
    /^corp\./i,
    /^lan\./i,
    /\.internal$/i,
    /\.local$/i,
    /\.localhost$/i,
  ];

  for (const pattern of internalPatterns) {
    if (pattern.test(lowerHost)) return true;
  }

  return false;
}

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

  // SSRF Protection: Block requests to private/internal hosts
  if (isPrivateHost(hostname)) {
    return new Response(
      JSON.stringify({
        error: "Forbidden",
        message: "Requests to private or internal hosts are not allowed",
      }),
      {
        status: 403,
        headers: { "Content-Type": "application/json" },
      }
    );
  }

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
