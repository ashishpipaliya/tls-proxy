package main

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"
	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

type Request struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	Profile string            `json:"profile,omitempty"` // e.g., chrome_133, chrome_120, firefox_117, etc.
	Proxy   string            `json:"proxy,omitempty"`   // Optional: http://proxy:port
}

type Response struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    interface{}       `json:"body"` // Changed to interface{} for dynamic JSON support
	Error   string            `json:"error,omitempty"`
}

// gzipResponseWriter wraps http.ResponseWriter to provide compression
type gzipResponseWriter struct {
	io.Writer
	http.ResponseWriter
}

func (w gzipResponseWriter) Write(b []byte) (int, error) {
	return w.Writer.Write(b)
}

// gzipMiddleware compresses the response if supported by the client
func gzipMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next(w, r)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		defer gz.Close()
		next(gzipResponseWriter{Writer: gz, ResponseWriter: w}, r)
	}
}

// recoveryMiddleware catches panics and returns a 500 JSON error
func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("Panic recovered: %v", err)
				sendError(w, http.StatusInternalServerError, "An internal server error occurred")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// Global client pool
var clientPool = make(map[string]tls_client.HttpClient)

func getOrCreateClient(profileName string, proxy string) (tls_client.HttpClient, error) {
	key := profileName + "|" + proxy

	if client, exists := clientPool[key]; exists {
		return client, nil
	}

	// Use the library's built-in map of profiles
	selectedProfile, exists := profiles.MappedTLSClients[profileName]
	if !exists {
		// Try case-insensitive lookup if direct match fails
		for k, v := range profiles.MappedTLSClients {
			if strings.EqualFold(k, profileName) {
				selectedProfile = v
				exists = true
				break
			}
		}
	}

	if !exists {
		log.Printf("Warning: Profile %s not found, falling back to chrome_133", profileName)
		selectedProfile = profiles.Chrome_133
	}

	jar := tls_client.NewCookieJar()
	options := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(30),
		tls_client.WithClientProfile(selectedProfile),
		tls_client.WithCookieJar(jar),
		tls_client.WithNotFollowRedirects(),
	}

	if proxy != "" {
		options = append(options, tls_client.WithProxyUrl(proxy))
	}

	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), options...)
	if err != nil {
		return nil, err
	}

	clientPool[key] = client
	return client, nil
}

func handleProfiles(w http.ResponseWriter, r *http.Request) {
	keys := make([]string, 0, len(profiles.MappedTLSClients))
	for k := range profiles.MappedTLSClients {
		keys = append(keys, k)
	}

	// Dynamic sorting: Browser name ascending, version descending
	sort.Slice(keys, func(i, j int) bool {
		p1, p2 := keys[i], keys[j]

		// Split into prefix and numeric parts
		prefix1, ver1 := splitProfile(p1)
		prefix2, ver2 := splitProfile(p2)

		if prefix1 != prefix2 {
			return prefix1 < prefix2 // Group by browser name
		}

		return ver1 > ver2 // Within same browser, latest version first
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"total":    len(keys),
		"profiles": keys,
	})
}

// splitProfile separates "chrome_133" into ("chrome", 133)
func splitProfile(s string) (string, int) {
	parts := strings.Split(s, "_")
	prefixParts := []string{}
	version := 0

	for _, p := range parts {
		// Try to find the version number
		v, err := strconv.Atoi(p)
		if err == nil {
			version = v
			// Once we find a number, the rest is usually descriptive (like PSK) or secondary version
			// We'll use the first number as the primary version
			break
		}
		prefixParts = append(prefixParts, p)
	}

	return strings.Join(prefixParts, "_"), version
}

func handleJSONRequest(w http.ResponseWriter, r *http.Request) {
	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, http.StatusBadRequest, err.Error())
		return
	}

	client, err := getOrCreateClient(req.Profile, req.Proxy)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "Failed to create TLS client: "+err.Error())
		return
	}

	var bodyReader io.Reader
	if req.Body != "" {
		bodyReader = strings.NewReader(req.Body)
	}

	httpReq, err := fhttp.NewRequest(req.Method, req.URL, bodyReader)
	if err != nil {
		sendError(w, http.StatusBadRequest, err.Error())
		return
	}

	headerOrder := make([]string, 0, len(req.Headers))
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
		headerOrder = append(headerOrder, k)
	}
	if len(headerOrder) > 0 {
		httpReq.Header[fhttp.HeaderOrderKey] = headerOrder
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		sendError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	responseHeaders := make(map[string]string)
	for k, v := range resp.Header {
		if len(v) > 0 {
			responseHeaders[k] = v[0]
		}
	}

	// Dynamic body parsing
	var finalBody interface{} = string(body)
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(contentType, "application/json") {
		var jsonBody interface{}
		if err := json.Unmarshal(body, &jsonBody); err == nil {
			finalBody = jsonBody
		}
	}

	response := Response{
		Status:  resp.StatusCode,
		Headers: responseHeaders,
		Body:    finalBody,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func sendError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(Response{
		Status: status,
		Error:  message,
	})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"service": "tls-bypass-proxy",
	})
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "online",
		"purpose": "High-performance API to bypass TLS fingerprinting by mimicking modern browser signatures.",
	})
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Register handlers on a mux
	mux := http.NewServeMux()
	mux.HandleFunc("/request", gzipMiddleware(handleJSONRequest))
	mux.HandleFunc("/profiles", gzipMiddleware(handleProfiles))
	mux.HandleFunc("/health", gzipMiddleware(handleHealth))
	mux.HandleFunc("/", handleRoot)

	// Wrap handlers with Recovery middleware
	handler := recoveryMiddleware(mux)

	// Check if running in AWS Lambda
	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		log.Println("🚀 TLS Bypass Service starting in AWS Lambda mode")
		lambda.Start(httpadapter.New(handler).ProxyWithContext)
		return
	}

	// Local development server
	log.Printf("🚀 TLS Bypass Service running on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, handler))
}
