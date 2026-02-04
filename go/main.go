package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fastly/compute-sdk-go/fsthttp"
)

// isPrivateHost checks if hostname is a private/internal address (SSRF protection)
func isPrivateHost(hostname string) bool {
	lowerHost := strings.ToLower(hostname)

	// Block localhost variants
	if lowerHost == "localhost" || lowerHost == "localhost.localdomain" {
		return true
	}

	// Block IPv6 localhost
	if lowerHost == "::1" || lowerHost == "[::1]" {
		return true
	}

	// Check for IPv4 address patterns
	parts := strings.Split(hostname, ".")
	if len(parts) == 4 {
		octets := make([]int, 4)
		valid := true
		for i, p := range parts {
			n, err := strconv.Atoi(p)
			if err != nil || n < 0 || n > 255 {
				valid = false
				break
			}
			octets[i] = n
		}

		if valid {
			a, b := octets[0], octets[1]

			// Loopback: 127.0.0.0/8
			if a == 127 {
				return true
			}

			// Private: 10.0.0.0/8
			if a == 10 {
				return true
			}

			// Private: 172.16.0.0/12
			if a == 172 && b >= 16 && b <= 31 {
				return true
			}

			// Private: 192.168.0.0/16
			if a == 192 && b == 168 {
				return true
			}

			// Link-local: 169.254.0.0/16 (includes AWS metadata endpoint)
			if a == 169 && b == 254 {
				return true
			}

			// Current network: 0.0.0.0/8
			if a == 0 {
				return true
			}
		}
	}

	// Block common internal hostnames
	internalPrefixes := []string{"internal.", "intranet.", "private.", "corp.", "lan."}
	for _, prefix := range internalPrefixes {
		if strings.HasPrefix(lowerHost, prefix) {
			return true
		}
	}

	internalSuffixes := []string{".internal", ".local", ".localhost"}
	for _, suffix := range internalSuffixes {
		if strings.HasSuffix(lowerHost, suffix) {
			return true
		}
	}

	return false
}

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

	// SSRF Protection: Block requests to private/internal hosts
	if isPrivateHost(hostname) {
		writeJSONError(w, fsthttp.StatusForbidden, "Forbidden", "Requests to private or internal hosts are not allowed")
		return
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
