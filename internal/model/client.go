// Package model defines the ModelClient contract and provider adapters. Per the
// doc, a ModelClient does exactly three things: convert internal messages+tools
// into a provider request, consume the provider stream into a stable assistant
// message, and surface raw stream events upward for UI/usage/debugging.
package model

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/review-fix-agent/rfa/internal/message"
)

// transientStatus reports whether an HTTP status code is worth retrying: gateway
// rate limits and transient upstream failures.
func transientStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}
	return false
}

// doWithRetry issues the request built by newReq, retrying on connection errors
// and transient HTTP statuses with exponential backoff. It only retries before
// any response body is consumed, so it is safe for the non-idempotent model
// endpoints (a transient failure produced no output). Backoff respects ctx
// cancellation. The caller owns the returned resp.Body.
func doWithRetry(ctx context.Context, hc *http.Client, newReq func() (*http.Request, error)) (*http.Response, error) {
	const maxAttempts = 4
	const maxBackoff = 30 * time.Second
	backoff := 500 * time.Millisecond
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
		req, err := newReq()
		if err != nil {
			return nil, err
		}
		resp, err := hc.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if attempt < maxAttempts-1 && transientStatus(resp.StatusCode) {
			// Honor a server-provided Retry-After (e.g. on 429) over our backoff.
			if ra := retryAfterDelay(resp); ra >= 0 {
				if ra > maxBackoff {
					ra = maxBackoff
				}
				backoff = ra
			}
			// Drain a bounded prefix so the connection can be reused, then retry.
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8*1024))
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("transient HTTP %d", resp.StatusCode)
			continue
		}
		return resp, nil
	}
	return nil, lastErr
}

// retryAfterDelay parses a Retry-After header expressed in integer seconds,
// returning a negative duration when it is absent or not a plain seconds value.
func retryAfterDelay(resp *http.Response) time.Duration {
	v := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if v == "" {
		return -1
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	return -1
}

// newStreamingHTTPClient returns an *http.Client tuned for streaming SSE. It
// bounds connection setup, TLS handshake, and time-to-first-byte so a hung or
// unreachable gateway fails fast instead of blocking scanner.Scan() forever.
// Crucially it sets no overall Timeout, which would otherwise abort a legitimate
// long-running stream mid-flight. Per-request cancellation still flows through
// the context passed to Stream.
func newStreamingHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 120 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
		},
	}
}

// ToolSchema is the provider-facing description of a tool.
type ToolSchema struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// Request is a provider-agnostic model request.
type Request struct {
	System      string
	Messages    []message.Message
	Tools       []ToolSchema
	Model       string
	MaxTokens   int
	Temperature float64
}

// StreamEventKind classifies a raw streaming event.
type StreamEventKind string

const (
	StreamText     StreamEventKind = "text"
	StreamThinking StreamEventKind = "thinking"
	// StreamToolInput signals incremental tool input arriving (not surfaced as
	// user-visible text; mostly useful for debugging).
	StreamToolInput StreamEventKind = "tool_input"
)

// StreamEvent is a raw, incremental event from the provider stream.
type StreamEvent struct {
	Kind StreamEventKind
	Text string
}

// Client is the unified model interface. Stream consumes the provider stream,
// invoking onEvent for each incremental event, and returns the fully aggregated
// assistant message plus token usage.
type Client interface {
	Stream(ctx context.Context, req Request, onEvent func(StreamEvent)) (message.Message, message.Usage, error)
	// Name identifies the provider (for logs/transcripts).
	Name() string
}
