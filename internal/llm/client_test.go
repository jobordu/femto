package llm

import (
	"encoding/json"
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

func TestCallFoldsSystemRoleOn500ThenSucceeds(t *testing.T) {
	// First call (with a system role) 500s "System role not supported"; femto folds
	// the system prompt into the user turn and retries — the second call succeeds and
	// must carry NO system role, with the system text prepended to the user content.
	var n int32
	var sawSystemOnRetry bool
	var userContent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := atomic.AddInt32(&n, 1)
		var body struct {
			Messages []Message `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if i == 1 {
			http.Error(w, `{"error":"System role not supported"}`, http.StatusInternalServerError)
			return
		}
		for _, m := range body.Messages {
			if m.Role == "system" {
				sawSystemOnRetry = true
			}
			if m.Role == "user" {
				userContent = m.Content
			}
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()
	rep, err := newTestClient(t, srv.URL+"/v1").Call([]Message{
		{Role: "system", Content: "SYS"}, {Role: "user", Content: "hi"}})
	if err != nil || rep.Content != "ok" {
		t.Fatalf("expected success after fold, got err=%v rep=%q", err, rep.Content)
	}
	if atomic.LoadInt32(&n) != 2 {
		t.Fatalf("expected 2 calls (500 then folded), got %d", n)
	}
	if sawSystemOnRetry {
		t.Error("retry still sent a system role")
	}
	if userContent != "SYS\n\nhi" {
		t.Errorf("system not folded into user turn: %q", userContent)
	}
}

func TestCallSystemRole500ButNoSystemMessage(t *testing.T) {
	// 500 mentioning "system role" but there's no system message to fold → no retry.
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		http.Error(w, "system role weirdness", http.StatusInternalServerError)
	}))
	defer srv.Close()
	_, err := newTestClient(t, srv.URL+"/v1").Call([]Message{{Role: "user", Content: "hi"}})
	if err == nil || atomic.LoadInt32(&n) != 1 {
		t.Fatalf("expected single error (nothing to fold), err=%v n=%d", err, n)
	}
}

func TestFoldSystemRole(t *testing.T) {
	// folds into the first user message
	got := foldSystemRole([]Message{{Role: "system", Content: "A"}, {Role: "user", Content: "B"}})
	if len(got) != 1 || got[0].Role != "user" || got[0].Content != "A\n\nB" {
		t.Errorf("fold-into-user wrong: %+v", got)
	}
	// multiple system messages joined; empty system dropped
	got = foldSystemRole([]Message{{Role: "system", Content: "A"}, {Role: "system", Content: ""}, {Role: "system", Content: "C"}, {Role: "user", Content: "B"}})
	if got[0].Content != "A\n\nC\n\nB" {
		t.Errorf("multi-system fold wrong: %q", got[0].Content)
	}
	// no user message → system promoted to a user turn
	got = foldSystemRole([]Message{{Role: "system", Content: "A"}, {Role: "assistant", Content: "X"}})
	if len(got) != 2 || got[0].Role != "user" || got[0].Content != "A" {
		t.Errorf("promote-system wrong: %+v", got)
	}
	// no system role → unchanged passthrough
	got = foldSystemRole([]Message{{Role: "user", Content: "B"}})
	if len(got) != 1 || got[0].Content != "B" {
		t.Errorf("no-system passthrough wrong: %+v", got)
	}
}

func TestHasSystemRole(t *testing.T) {
	if !hasSystemRole([]Message{{Role: "system"}}) || hasSystemRole([]Message{{Role: "user"}}) {
		t.Error("hasSystemRole wrong")
	}
}

func TestCallRetriesOnTemperatureZeroRejection(t *testing.T) {
	// First call (temp 0) 422s "temperature must be > 0"; femto retries with a small
	// positive temp so a deterministic run doesn't exclude the model.
	var n int32
	var retryTemp float64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := atomic.AddInt32(&n, 1)
		var body struct {
			Temperature float64 `json:"temperature"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if i == 1 {
			http.Error(w, `{"error":"body -> temperature: Input should be greater than 0"}`,
				http.StatusUnprocessableEntity)
			return
		}
		retryTemp = body.Temperature
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()
	rep, err := newTestClient(t, srv.URL+"/v1").Call([]Message{{Role: "user", Content: "hi"}})
	if err != nil || rep.Content != "ok" || atomic.LoadInt32(&n) != 2 {
		t.Fatalf("expected retry+success, err=%v rep=%q n=%d", err, rep.Content, n)
	}
	if retryTemp <= 0 {
		t.Errorf("retry temperature not positive: %v", retryTemp)
	}
}

func TestCallChainsTempThenSystemRole(t *testing.T) {
	// gemma at temp 0: 422 temperature, then 500 system-role, then success — both
	// fix-ups chain. Final request must have temp>0 AND no system role.
	var n int32
	var lastTemp float64
	var lastHadSystem bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := atomic.AddInt32(&n, 1)
		var body struct {
			Temperature float64   `json:"temperature"`
			Messages    []Message `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		lastTemp = body.Temperature
		lastHadSystem = false
		for _, m := range body.Messages {
			if m.Role == "system" {
				lastHadSystem = true
			}
		}
		switch i {
		case 1:
			http.Error(w, `{"error":"temperature must be greater than 0"}`, http.StatusUnprocessableEntity)
		case 2:
			http.Error(w, `{"error":"System role not supported"}`, http.StatusInternalServerError)
		default:
			w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
		}
	}))
	defer srv.Close()
	rep, err := newTestClient(t, srv.URL+"/v1").Call([]Message{
		{Role: "system", Content: "S"}, {Role: "user", Content: "hi"}})
	if err != nil || rep.Content != "ok" || atomic.LoadInt32(&n) != 3 {
		t.Fatalf("expected chained fixups, err=%v rep=%q n=%d", err, rep.Content, n)
	}
	if lastTemp <= 0 || lastHadSystem {
		t.Errorf("final request wrong: temp=%v hadSystem=%v", lastTemp, lastHadSystem)
	}
}

func TestCallSendsStopSequences(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Stop []string `json:"stop"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		got = body.Stop
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL+"/v1")
	c.Stop = []string{"\nOUTPUT:", "\nObservation:"}
	if _, err := c.Call([]Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "\nOUTPUT:" || got[1] != "\nObservation:" {
		t.Fatalf("stop not sent: %q", got)
	}
}

func TestCallOmitsStopWhenUnset(t *testing.T) {
	var raw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&raw)
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()
	if _, err := newTestClient(t, srv.URL+"/v1").Call([]Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["stop"]; ok {
		t.Fatalf("stop must be omitted when unset: %v", raw)
	}
}

func TestCallDropsStopWhenRejected(t *testing.T) {
	// Reasoning models reject the parameter; femto resends once without it rather than
	// excluding the model.
	var n int32
	var retryHadStop bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := atomic.AddInt32(&n, 1)
		var raw map[string]any
		json.NewDecoder(r.Body).Decode(&raw)
		if i == 1 {
			http.Error(w, `{"error":{"message":"Unsupported parameter: 'stop' is not supported with this model."}}`,
				http.StatusBadRequest)
			return
		}
		_, retryHadStop = raw["stop"]
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL+"/v1")
	c.Stop = []string{"\nOUTPUT:"}
	rep, err := c.Call([]Message{{Role: "user", Content: "hi"}})
	if err != nil || rep.Content != "ok" || atomic.LoadInt32(&n) != 2 || retryHadStop {
		t.Fatalf("want one stop-less retry, err=%v rep=%q n=%d retryHadStop=%v", err, rep.Content, n, retryHadStop)
	}
	if c.Stop == nil {
		t.Error("the fix-up must not mutate the client's configured Stop")
	}
}
