package llm

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, url string) *Client {
	t.Helper()
	c, err := New(url, "k", "m", 0.0, 64, 5*time.Second, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	c.maxRetries = 3
	return c
}

func TestCallReturnsContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("missing auth header")
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"hello"}}]}`))
	}))
	defer srv.Close()
	rep, err := newTestClient(t, srv.URL+"/v1").Call([]Message{{Role: "user", Content: "hi"}})
	if err != nil || rep.Content != "hello" {
		t.Fatalf("got %q err=%v", rep.Content, err)
	}
}

func TestCallReasoningFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"content":"","reasoning_content":"r"}}]}`))
	}))
	defer srv.Close()
	rep, _ := newTestClient(t, srv.URL+"/v1").Call(nil)
	if rep.Content != "r" {
		t.Fatalf("reasoning fallback failed: %q", rep.Content)
	}
}

func TestCallNativeToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"content":"","tool_calls":[{"id":"c1","function":{"name":"shell","arguments":"{\"input\":\"ls\"}"}}]}}]}`))
	}))
	defer srv.Close()
	rep, _ := newTestClient(t, srv.URL+"/v1").Call(nil)
	if len(rep.ToolCalls) != 1 || rep.ToolCalls[0].Function.Name != "shell" {
		t.Fatalf("tool_calls not parsed: %+v", rep.ToolCalls)
	}
}

func TestCallBacksOffOn429ThenSucceeds(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) <= 2 { // fail twice, then succeed
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()
	rep, err := newTestClient(t, srv.URL+"/v1").Call(nil)
	if err != nil || rep.Content != "ok" || atomic.LoadInt32(&n) != 3 {
		t.Fatalf("expected 3 attempts to ok, got %q err=%v n=%d", rep.Content, err, n)
	}
}

func TestCallReturnsErrorOn500(t *testing.T) {
	// 500 is NOT retried (deterministic on this tier) → surfaces immediately
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		http.Error(w, "engine core", http.StatusInternalServerError)
	}))
	defer srv.Close()
	_, err := newTestClient(t, srv.URL+"/v1").Call(nil)
	if err == nil || atomic.LoadInt32(&n) != 1 {
		t.Fatalf("expected single 500 error, got err=%v n=%d", err, n)
	}
}
