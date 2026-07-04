package llm

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewWithProxy(t *testing.T) {
	c, err := New("http://x/v1", "k", "m", 0, 16, time.Second, "http://127.0.0.1:9", nil)
	if err != nil || c.http.Transport == nil {
		t.Fatalf("proxy transport not set: %v", err)
	}
}

func TestNewInvalidProxy(t *testing.T) {
	if _, err := New("http://x/v1", "k", "m", 0, 16, time.Second, "://bad", nil); err == nil {
		t.Fatal("expected invalid-proxy error")
	}
}

func TestCallTransportErrorAfterRetries(t *testing.T) {
	// unreachable host + tiny timeout → Do() errors; maxRetries=1 → 1 retry then surface
	c, _ := New("http://127.0.0.1:1/v1", "k", "m", 0, 16, 100*time.Millisecond, "", nil)
	c.maxRetries = 1
	if _, err := c.Call(nil); err == nil {
		t.Fatal("expected transport error")
	}
}

func TestParseReplyBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`not json`))
	}))
	defer srv.Close()
	c, _ := New(srv.URL+"/v1", "k", "m", 0, 16, time.Second, "", nil)
	if _, err := c.Call(nil); err == nil {
		t.Fatal("expected JSON parse error")
	}
}

func TestParseReplyEmptyChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[]}`))
	}))
	defer srv.Close()
	c, _ := New(srv.URL+"/v1", "k", "m", 0, 16, time.Second, "", nil)
	rep, err := c.Call(nil)
	if err != nil || rep.Content != "" || len(rep.ToolCalls) != 0 {
		t.Fatalf("empty-choices should be empty reply: %+v err=%v", rep, err)
	}
}

func TestCallErrorBodyTruncated(t *testing.T) {
	// >200-char error body exercises the truncate(<n) shortening branch
	long := make([]byte, 500)
	for i := range long {
		long[i] = 'x'
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write(long)
	}))
	defer srv.Close()
	c, _ := New(srv.URL+"/v1", "k", "m", 0, 16, time.Second, "", nil)
	_, err := c.Call(nil)
	if err == nil || len(err.Error()) > 260 { // "llm http 400: " + 200 chars
		t.Fatalf("error body not truncated: %v", err)
	}
}

func TestCallWithToolsBody(t *testing.T) {
	// a client configured with tool schemas exercises the native-body branch
	var sawTools bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		sawTools = contains(string(buf[:n]), `"tools"`)
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()
	schemas := []any{map[string]any{"type": "function"}}
	c, _ := New(srv.URL+"/v1", "k", "m", 0, 16, time.Second, "", schemas)
	if _, err := c.Call(nil); err != nil || !sawTools {
		t.Fatalf("tools not sent: sawTools=%v err=%v", sawTools, err)
	}
}

func TestCallNewRequestError(t *testing.T) {
	// a control char in the URL makes http.NewRequest fail inside postRetry
	c, _ := New("http://exa\x7fmple/v1", "k", "m", 0, 16, time.Second, "", nil)
	if _, err := c.Call(nil); err == nil {
		t.Fatal("expected NewRequest error")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestTruncateShortString(t *testing.T) {
	if got := truncate("abc", 10); got != "abc" { // len <= n branch
		t.Fatalf("truncate short changed value: %q", got)
	}
}
