package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"sync"
	"time"
)

var (
	logWriter     io.Writer
	maxRetries    = 3
	retryInterval = 60 * time.Second
	semaphore     = make(chan struct{}, 3)
	totalRequests = 0
	mutex         = sync.Mutex{}

	apiKeys     = []string{}
	apiKeyIndex = 0
)

func init() {
	logFile, err := os.OpenFile("main.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("Failed to open log file: %v\n", err)
		os.Exit(1)
	}

	logWriter = io.MultiWriter(os.Stdout, logFile)
}

func createProxyHandler(targetURL *url.URL) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		proxyHandler(w, r, targetURL)
	}
}

func proxyHandler(w http.ResponseWriter, r *http.Request, targetURL *url.URL) {
	mutex.Lock()
	totalRequests++
	currentRequest := totalRequests
	fmt.Fprintf(logWriter, "Total requests: %d\n", totalRequests)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Unable to read request body", http.StatusInternalServerError)
		return
	}
	r.Body.Close()
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	fmt.Fprintf(logWriter, "\n")
	mutex.Unlock()

	semaphore <- struct{}{}
	defer func() { <-semaphore }()

	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = targetURL.Host

		mutex.Lock()
		// req.Header.Set("Authorization", apiKeys[apiKeyIndex])
		apiKeyIndex = (apiKeyIndex + 1) % len(apiKeys)
		mutex.Unlock()

		mutex.Lock()
		fmt.Fprintf(logWriter, "Request index: #%d:\n", currentRequest)
		fmt.Fprintf(logWriter, "Going through Director\n")
		fmt.Fprintf(logWriter, "Method: %s\n", req.Method)
		fmt.Fprintf(logWriter, "URL: %s\n", req.URL)
		fmt.Fprintf(logWriter, "HTTP version: %s\n", req.Proto)
		fmt.Fprintf(logWriter, "Headers:\n")
		for key, values := range req.Header {
			for _, value := range values {
				fmt.Fprintf(logWriter, "%s: %s\n", key, value)
			}
		}
		if req.Body != nil {
			reqBody, _ := io.ReadAll(req.Body)
			req.Body.Close()
			fmt.Fprintf(logWriter, "Body:\n%s\n", string(reqBody))
			req.Body = io.NopCloser(bytes.NewBuffer(reqBody))
		}
		fmt.Fprintf(logWriter, "\n")
		mutex.Unlock()
	}

	proxy.ModifyResponse = func(resp *http.Response) error {
		mutex.Lock()
		fmt.Fprintf(logWriter, "Response index: #%d:\n", currentRequest)
		fmt.Fprintf(logWriter, "Going through ModifyResponse\n")
		fmt.Fprintf(logWriter, "HTTP version: %s\n", resp.Proto)
		fmt.Fprintf(logWriter, "Status: %s\n", resp.Status)
		fmt.Fprintf(logWriter, "Status Code: %d\n", resp.StatusCode)
		fmt.Fprintf(logWriter, "Headers:\n")
		for key, values := range resp.Header {
			for _, value := range values {
				fmt.Fprintf(logWriter, "%s: %s\n", key, value)
			}
		}
		if resp.Body != nil {
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			fmt.Fprintf(logWriter, "Body:\n%s\n", string(respBody))
			resp.Body = io.NopCloser(bytes.NewBuffer(respBody))
		}
		fmt.Fprintf(logWriter, "\n")
		mutex.Unlock()

		return nil
	}

	// proxyURL, _ := url.Parse("http://127.0.0.1:7890")
	// proxy.Transport = &http.Transport{
	// 	Proxy: http.ProxyURL(proxyURL),
	// }

	for i := 0; i < maxRetries; i++ {
		proxyReq := r.Clone(r.Context())
		proxyReq.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		proxyReq.ContentLength = int64(len(bodyBytes))

		rw := &responseWriter{ResponseWriter: w}
		proxy.ServeHTTP(rw, proxyReq)

		if rw.statusCode >= 200 && rw.statusCode < 300 {

			return
		}

		mutex.Lock()
		fmt.Fprintf(logWriter, "Response index: #%d:\n", currentRequest)
		fmt.Fprintf(logWriter, "Proxy request failed, attempting retry %d of %d\n", i+1, maxRetries)
		fmt.Fprintf(logWriter, "\n")
		mutex.Unlock()
		time.Sleep(retryInterval)
	}

	mutex.Lock()
	fmt.Fprintf(logWriter, "Response index: #%d:\n", currentRequest)
	fmt.Fprintf(logWriter, "All retry attempts failed, program will exit\n")
	fmt.Fprintf(logWriter, "\n")
	mutex.Unlock()

	os.Exit(1)
}

func main() {
	targetURL, _ := url.Parse("http://127.0.0.1:8081")
	http.HandleFunc("/", createProxyHandler(targetURL))
	fmt.Fprintf(logWriter, "Starting proxy server on :8082\n")
	if err := http.ListenAndServe(":8082", nil); err != nil {
		fmt.Fprintf(logWriter, "Failed to start server: %v\n", err)
	}
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(statusCode int) {
	rw.statusCode = statusCode
	rw.ResponseWriter.WriteHeader(statusCode)
}
