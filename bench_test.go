package idempo_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/eben-vranken/idempo"
	"github.com/eben-vranken/idempo/inmem"
)

var benchNext = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	w.Write([]byte(`{"ok":true}`))
})

var benchBody = []byte(`{"order_id":123,"amount":4200,"currency":"usd"}`)

// BenchmarkBareHandler is the baseline: the handler with no middleware.
func BenchmarkBareHandler(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/charge", bytes.NewReader(benchBody))
		benchNext.ServeHTTP(httptest.NewRecorder(), req)
	}
}

// BenchmarkPassthrough measures the middleware cost for a request that carries
// no Idempotency-Key (the middleware does nothing but check the header).
func BenchmarkPassthrough(b *testing.B) {
	handler := idempo.New(inmem.New(24*time.Hour, 5*time.Minute), idempo.Options{}).Handler(benchNext)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/charge", bytes.NewReader(benchBody))
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}
}

// BenchmarkReplay measures the cached-replay path: a request whose key has
// already been completed, so the stored response is returned without running
// the handler.
func BenchmarkReplay(b *testing.B) {
	handler := idempo.New(inmem.New(24*time.Hour, 5*time.Minute), idempo.Options{}).Handler(benchNext)

	const key = "bench-replayed-key"
	prime := httptest.NewRequest(http.MethodPost, "/charge", bytes.NewReader(benchBody))
	prime.Header.Set("Idempotency-Key", key)
	handler.ServeHTTP(httptest.NewRecorder(), prime)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/charge", bytes.NewReader(benchBody))
		req.Header.Set("Idempotency-Key", key)
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}
}
