package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/fastly/compute-sdk-go/fsthttp"
)

func main() {
	fsthttp.ServeFunc(handleRequest)
}

func handleRequest(ctx context.Context, w fsthttp.ResponseWriter, r *fsthttp.Request) {
	reqURL, err := url.Parse(r.URL.String())
	if err != nil {
		writeJSONError(w, fsthttp.StatusBadRequest, "Invalid request URL", err.Error())
		return
	}

	// Validate API key
	apiKey := reqURL.Query().Get("key")
	if apiKey != "testing" {
		writeJSONError(w, fsthttp.StatusForbidden, "Unauthorized", "Invalid or missing API key")
		return
	}

	// Get the target URL from the query parameter
	targetURLParam := reqURL.Query().Get("url")
	if targetURLParam == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(fsthttp.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Missing 'url' query parameter",
			"usage": "Add ?url=https://example.com/path to your request",
		})
		return
	}

	// Parse the target URL
	targetURL, err := url.Parse(targetURLParam)
	if err != nil {
		writeJSONError(w, fsthttp.StatusBadRequest, "Invalid URL provided", err.Error())
		return
	}

	// Only allow https protocol (TLS backends only)
	if targetURL.Scheme != "https" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(fsthttp.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Only https URLs are supported",
			"usage": "Use https:// URLs (e.g., ?url=https://example.com/path)",
		})
		return
	}

	hostname := targetURL.Hostname()
	port := targetURL.Port()
	if port == "" {
		port = "443"
	}

	// Create a unique backend name based on host and port
	// Backend names must be alphanumeric with underscores/hyphens
	re := regexp.MustCompile(`[^a-zA-Z0-9]`)
	sanitizedHostname := re.ReplaceAllString(hostname, "_")
	backendName := fmt.Sprintf("dyn_%s_%s", sanitizedHostname, port)

	// Create backend options with TLS
	opts := fsthttp.NewBackendOptions().
		HostOverride(hostname).
		UseSSL(true).
		SSLMinVersion(fsthttp.TLSVersion1_2).
		SSLMaxVersion(fsthttp.TLSVersion1_3).
		SNIHostname(hostname).
		CertHostname(hostname).
		ConnectTimeout(10 * time.Second).
		FirstByteTimeout(30 * time.Second).
		BetweenBytesTimeout(30 * time.Second)

	// Create the dynamic backend
	backend, err := fsthttp.RegisterDynamicBackend(backendName, fmt.Sprintf("%s:%s", hostname, port), opts)
	if err != nil {
		writeJSONErrorWithTarget(w, fsthttp.StatusBadGateway, "Failed to create backend", err.Error(), targetURLParam)
		return
	}

	// Build the request to the origin
	// Preserve the path and query string from the target URL
	originPath := targetURL.Path
	if targetURL.RawQuery != "" {
		originPath = originPath + "?" + targetURL.RawQuery
	}
	if originPath == "" {
		originPath = "/"
	}

	// Create a new request to the origin
	originReq, err := fsthttp.NewRequest(r.Method, originPath, r.Body)
	if err != nil {
		writeJSONErrorWithTarget(w, fsthttp.StatusBadGateway, "Failed to create origin request", err.Error(), targetURLParam)
		return
	}

	// Copy headers from original request
	for name, values := range r.Header {
		// Skip headers that shouldn't be forwarded
		nameLower := strings.ToLower(name)
		if nameLower == "x-forwarded-for" ||
			nameLower == "x-forwarded-host" ||
			nameLower == "x-forwarded-proto" ||
			nameLower == "host" {
			continue
		}
		for _, value := range values {
			originReq.Header.Add(name, value)
		}
	}

	// Set the host header to match the target
	originReq.Header.Set("Host", hostname)

	// Set cache override to pass (don't cache)
	originReq.CacheOptions.Pass = true

	// Fetch from the dynamic backend
	resp, err := originReq.Send(ctx, backend.Name())
	if err != nil {
		writeJSONErrorWithTarget(w, fsthttp.StatusBadGateway, "Failed to fetch from origin", err.Error(), targetURLParam)
		return
	}

	// Copy response headers
	for name, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}

	// Write status code and body
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func writeJSONError(w fsthttp.ResponseWriter, status int, errorMsg, details string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{
		"error":   errorMsg,
		"message": details,
	})
}

func writeJSONErrorWithTarget(w fsthttp.ResponseWriter, status int, errorMsg, details, target string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{
		"error":   errorMsg,
		"details": details,
		"target":  target,
	})
}
