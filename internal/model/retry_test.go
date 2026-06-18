package model

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func newReqFn(url string) func() (*http.Request, error) {
	return func() (*http.Request, error) {
		return http.NewRequest(http.MethodPost, url, http.NoBody)
	}
}

func TestDoWithRetrySucceedsAfterTransient(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "ok")
	}))
	defer srv.Close()

	resp, err := doWithRetry(context.Background(), srv.Client(), newReqFn(srv.URL))
	if err != nil {
		t.Fatalf("doWithRetry: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 after retries", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("calls = %d, want 3 (2 transient + 1 success)", got)
	}
}

func TestDoWithRetryGivesUpAndReturnsLast(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	resp, err := doWithRetry(context.Background(), srv.Client(), newReqFn(srv.URL))
	if err != nil {
		t.Fatalf("doWithRetry returned err: %v", err)
	}
	defer resp.Body.Close()
	// All attempts transient → returns the final response (429) for the caller
	// to surface, rather than retrying forever.
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 4 {
		t.Errorf("calls = %d, want 4 (maxAttempts)", got)
	}
}

func TestDoWithRetryDoesNotRetryNonTransient(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest) // 400 is not transient
	}))
	defer srv.Close()

	resp, err := doWithRetry(context.Background(), srv.Client(), newReqFn(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("calls = %d, want 1 (no retry on 400)", got)
	}
}

func TestDoWithRetryRespectsContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := doWithRetry(ctx, srv.Client(), newReqFn(srv.URL))
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestDoWithRetryHonorsRetryAfter(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "1") // ask for a 1s wait
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	start := time.Now()
	resp, err := doWithRetry(context.Background(), srv.Client(), newReqFn(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	// Must have waited ~1s per Retry-After, not the 500ms default first backoff.
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Errorf("Retry-After not honored: waited only %s", elapsed)
	}
}
