package idempo_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eben-vranken/idempo"
	"github.com/eben-vranken/idempo/inmem"
)

func TestKeyValidation(t *testing.T) {
	cases := []struct {
		name       string
		key        string
		wantCode   int
		nextCalled bool
	}{
		{
			name:       "Empty key passes through (no idempotency)",
			key:        "",
			wantCode:   200,
			nextCalled: true,
		},
		{
			name:       "Short opaque key",
			key:        "abc",
			wantCode:   200,
			nextCalled: true,
		},
		{
			name:       "Arbitrary opaque key",
			key:        "order-12345",
			wantCode:   200,
			nextCalled: true,
		},
		{
			name:       "UUIDv4 is accepted",
			key:        "123e4567-e89b-42d3-a456-426614174000",
			wantCode:   200,
			nextCalled: true,
		},
		{
			name:       "UUIDv7 is accepted",
			key:        "019e705d-bb1a-7085-9c1b-58a6a14a1aeb",
			wantCode:   200,
			nextCalled: true,
		},
		{
			name:       "Max length key (255) is accepted",
			key:        strings.Repeat("a", 255),
			wantCode:   200,
			nextCalled: true,
		},
		{
			name:       "Over-long key (256) is rejected",
			key:        strings.Repeat("a", 256),
			wantCode:   400,
			nextCalled: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
			})

			m := idempo.New(inmem.New(24*time.Hour, 5*time.Minute), idempo.Options{})
			handler := m.Handler(next)

			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req.Header.Set("Idempotency-Key", tc.key)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantCode {
				t.Errorf("got status %d, want %d", rec.Code, tc.wantCode)
			}

			if called != tc.nextCalled {
				t.Errorf("next called = %v, want %v", called, tc.nextCalled)
			}
		})
	}
}

func TestBodyIsRestoredAfterRead(t *testing.T) {
	jsonRequest := []byte(`{order_id:123, "status": "created"}`)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(jsonRequest))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "019e705d-bb1a-7085-9c1b-58a6a14a1aeb")
	rec := httptest.NewRecorder()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !bytes.Equal(body, jsonRequest) {
			t.Errorf("Body returned = %08b, requested %08b", body, jsonRequest)
		}
	})

	m := idempo.New(inmem.New(24*time.Hour, 5*time.Minute), idempo.Options{})
	handler := m.Handler(next)

	handler.ServeHTTP(rec, req)
}

func TestHandlerReplayResponse(t *testing.T) {
	jsonRequest := []byte(`{order_id:123, "status": "created"}`)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(jsonRequest))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "019e705d-bb1a-7085-9c1b-58a6a14a1aeb")
	rec := httptest.NewRecorder()

	var body []byte

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("X-Custom", "42")
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusCreated)
		w.Write(body)
	})

	m := idempo.New(inmem.New(24*time.Hour, 5*time.Minute), idempo.Options{})
	handler := m.Handler(next)
	handler.ServeHTTP(rec, req)

	req = httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(jsonRequest))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "019e705d-bb1a-7085-9c1b-58a6a14a1aeb")
	rec2 := httptest.NewRecorder()

	handler = m.Handler(next)
	handler.ServeHTTP(rec2, req)

	if rec2.Code != 201 {
		t.Errorf("Code returned = %d, expected %d", rec2.Code, 201)
	}

	if rec2.Body.String() != string(body) {
		t.Errorf("Body returned = %s, expected %s", rec2.Body.String(), `{order_id:123, "status": "created"}`)
	}

	if rec2.Header().Get("Idempotency-Replayed") != "true" {
		t.Errorf("Idempotency Returned = %s, expected %s", rec2.Header().Get("Idempotency-Replayed"), "true")
	}

	if rec2.Header().Get("X-Custom") != "42" {
		t.Errorf("Idempotency Returned = %s, expected %s", rec2.Header().Get("X-Custom"), "42")
	}

	if rec2.Header().Get("Content-Type") != "text/plain" {
		t.Errorf("Idempotency Returned = %s, expected %s", rec2.Header().Get("Content-Type"), "text/plain")
	}
}

func TestHandlerReplayLargeBody(t *testing.T) {
	jsonRequest := []byte(`{order_id:123, "status": "created"}`)
	bigJsonRequest := []byte(`{
		"user": "test_user_12345",
		"request_type": "demo_payload_generation",
		"description": "This is a sample JSON object created specifically to exceed the minimum byte requirement of 128 bytes for testing purposes.",
		"data": {
			"items": [
			"alpha",
			"beta",
			"gamma",
			"delta",
			"epsilon"
			],
			"settings": {
			"mode": "extended",
			"debug": true,
			"version": "1.0.3"
			}
		},
		"timestamp": "2026-06-02T12:00:00Z",
		"notes": "Extra padding text to ensure the payload comfortably exceeds the required byte threshold."
		}`)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(bigJsonRequest))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "019e705d-bb1a-7085-9c1b-58a6a14a1aeb")
	rec := httptest.NewRecorder()

	var body []byte

	count := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("X-Custom", "42")
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusCreated)
		w.Write(body)
	})

	m := idempo.New(inmem.New(24*time.Hour, 5*time.Minute), idempo.Options{})
	handler := m.Handler(next)
	handler.ServeHTTP(rec, req)

	req = httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(bigJsonRequest))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "019e705d-bb1a-7085-9c1b-58a6a14a1aeb")
	rec2 := httptest.NewRecorder()

	handler = m.Handler(next)
	handler.ServeHTTP(rec2, req)

	if rec2.Code != 201 {
		t.Errorf("Code returned = %d, expected %d", rec2.Code, 201)
	}

	if count != 1 {
		t.Errorf("Count returned = %d, expected %d", count, 1)
	}

	if rec2.Body.String() != string(body) {
		t.Errorf("Body returned = %s, expected %s", rec2.Body.String(), `{order_id:123, "status": "created"}`)
	}

	if rec2.Header().Get("Idempotency-Replayed") != "true" {
		t.Errorf("Idempotency Returned = %s, expected %s", rec2.Header().Get("Idempotency-Replayed"), "true")
	}

	if rec2.Header().Get("X-Custom") != "42" {
		t.Errorf("Idempotency Returned = %s, expected %s", rec2.Header().Get("X-Custom"), "42")
	}

	if rec2.Header().Get("Content-Type") != "text/plain" {
		t.Errorf("Idempotency Returned = %s, expected %s", rec2.Header().Get("Content-Type"), "text/plain")
	}

	req = httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(jsonRequest))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "019e705d-bb1a-7085-9c1b-58a6a14a1aeb")
	rec3 := httptest.NewRecorder()

	handler = m.Handler(next)
	handler.ServeHTTP(rec3, req)

	if rec3.Code != 422 {
		t.Errorf("Code returned = %d, expected %d", rec3.Code, 422)
	}

	if count != 1 {
		t.Errorf("Count returned = %d, expected %d", count, 1)
	}
}

func TestHandlerReplayResponseWithMismatchedBody(t *testing.T) {
	jsonRequest := []byte(`{order_id:123, "status": "created"}`)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(jsonRequest))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "019e705d-bb1a-7085-9c1b-58a6a14a1aeb")
	rec := httptest.NewRecorder()

	var body []byte

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		w.Write(body)
	})

	m := idempo.New(inmem.New(24*time.Hour, 5*time.Minute), idempo.Options{})
	handler := m.Handler(next)
	handler.ServeHTTP(rec, req)

	differentJsonRequest := []byte(`{order_id:124, "status": "created"}`)

	req = httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(differentJsonRequest))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "019e705d-bb1a-7085-9c1b-58a6a14a1aeb")
	rec2 := httptest.NewRecorder()

	handler = m.Handler(next)
	handler.ServeHTTP(rec2, req)

	if rec2.Code != 422 {
		t.Errorf("Code returned = %d, expected %d", rec2.Code, 422)
	}
}

func TestHandlerProcessesExpiredKeyAsFresh(t *testing.T) {
	jsonRequest := []byte(`{order_id:123, "status": "created"}`)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(jsonRequest))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "019e705d-bb1a-7085-9c1b-58a6a14a1aeb")
	rec := httptest.NewRecorder()

	var body []byte

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		w.Write(body)
	})

	m := idempo.New(inmem.New(1*time.Millisecond, 5*time.Millisecond), idempo.Options{})
	handler := m.Handler(next)
	handler.ServeHTTP(rec, req)

	time.Sleep(time.Millisecond * 10)

	req = httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(jsonRequest))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "019e705d-bb1a-7085-9c1b-58a6a14a1aeb")
	rec2 := httptest.NewRecorder()

	handler = m.Handler(next)
	handler.ServeHTTP(rec2, req)

	if rec2.Code != 201 {
		t.Errorf("Code returned = %d, expected %d", rec2.Code, 201)
	}

	if rec2.Header().Get("Idempotency-Replayed") == "true" {
		t.Errorf("Idempotency Returned = %s, expected %s", rec2.Header().Get("Idempotency-Replayed"), "false")
	}
}

func TestHandlerReturnsConflictForInFlightRequest(t *testing.T) {
	block := make(chan struct{})
	jsonRequest := []byte(`{order_id:123, "status": "created"}`)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(jsonRequest))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "019e705d-bb1a-7085-9c1b-58a6a14a1aeb")
	rec := httptest.NewRecorder()

	var body []byte

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		w.Write(body)
		<-block
	})

	m := idempo.New(inmem.New(24*time.Hour, 5*time.Minute), idempo.Options{})
	handler := m.Handler(next)
	go handler.ServeHTTP(rec, req)

	time.Sleep(time.Millisecond * 10)

	req = httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(jsonRequest))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "019e705d-bb1a-7085-9c1b-58a6a14a1aeb")
	rec2 := httptest.NewRecorder()

	handler = m.Handler(next)
	handler.ServeHTTP(rec2, req)

	if rec2.Code != 409 {
		t.Errorf("Code returned = %d, expected %d", rec2.Code, 409)
	}

	close(block)
}

func TestHandler5xxReleasesClaim(t *testing.T) {
	jsonRequest := []byte(`{order_id:123, "status": "created"}`)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(jsonRequest))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "019e705d-bb1a-7085-9c1b-58a6a14a1aeb")
	rec := httptest.NewRecorder()

	var body []byte
	count := 0

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write(body)
	})

	m := idempo.New(inmem.New(24*time.Hour, 5*time.Minute), idempo.Options{})
	handler := m.Handler(next)
	handler.ServeHTTP(rec, req)

	req = httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(jsonRequest))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "019e705d-bb1a-7085-9c1b-58a6a14a1aeb")
	rec2 := httptest.NewRecorder()

	handler = m.Handler(next)
	handler.ServeHTTP(rec2, req)

	if rec2.Code != 500 {
		t.Errorf("Code returned = %d, expected %d", rec2.Code, 500)
	}

	if count != 2 {
		t.Errorf("Count returned = %d, expected %d", count, 2)
	}

	if rec2.Header().Get("Idempotency-Replayed") == "true" {
		t.Errorf("Idempotency Returned = %s, expected %s", rec2.Header().Get("Idempotency-Replayed"), "false")
	}
}

func TestHandlerPanicReleaseClaim(t *testing.T) {
	jsonRequest := []byte(`{order_id:123, "status": "created"}`)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(jsonRequest))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "019e705d-bb1a-7085-9c1b-58a6a14a1aeb")
	rec := httptest.NewRecorder()

	var body []byte
	count := 0

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		if count == 1 {
			panic("boom")
		}
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	})

	m := idempo.New(inmem.New(24*time.Hour, 5*time.Minute), idempo.Options{})
	handler := m.Handler(next)

	func() {
		defer func() {
			if rec := recover(); rec == nil {
				t.Error("expected panic to propagate, got none")
			}
		}()
		handler.ServeHTTP(rec, req)
	}()

	req = httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(jsonRequest))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "019e705d-bb1a-7085-9c1b-58a6a14a1aeb")
	rec2 := httptest.NewRecorder()

	handler = m.Handler(next)
	handler.ServeHTTP(rec2, req)

	if rec2.Code != 200 {
		t.Errorf("Code returned = %d, expected %d", rec2.Code, 200)
	}

	if count != 2 {
		t.Errorf("Count returned = %d, expected %d", count, 2)
	}

	if rec2.Header().Get("Idempotency-Replayed") == "true" {
		t.Errorf("Idempotency Returned = %s, expected %s", rec2.Header().Get("Idempotency-Replayed"), "false")
	}
}

func TestHandlerPersistsAfterSlowHandler(t *testing.T) {
	store := &fakeStore{claimResult: idempo.ClaimResult{Status: idempo.StatusNew}}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusCreated)
	})

	handler := idempo.New(store, idempo.Options{PersistentTimeout: 50 * time.Millisecond}).Handler(next)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("{}"))
	req.Header.Set("Idempotency-Key", "019e705d-bb1a-7085-9c1b-58a6a14a1aeb")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !store.completed {
		t.Error("store.completed = false: persist context expired before Complete ran")
	}
}

func TestHandlerSameKeyDifferentPathConflicts(t *testing.T) {
	jsonRequest := []byte(`{order_id:123, "status": "created"}`)

	req := httptest.NewRequest(http.MethodPost, "/charges", bytes.NewBuffer(jsonRequest))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "019e705d-bb1a-7085-9c1b-58a6a14a1aeb")
	rec := httptest.NewRecorder()

	var body []byte

	count := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		w.Write(body)
	})

	m := idempo.New(inmem.New(24*time.Hour, 5*time.Minute), idempo.Options{})
	handler := m.Handler(next)
	handler.ServeHTTP(rec, req)

	req = httptest.NewRequest(http.MethodPost, "/refunds", bytes.NewBuffer(jsonRequest))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "019e705d-bb1a-7085-9c1b-58a6a14a1aeb")
	rec2 := httptest.NewRecorder()

	handler = m.Handler(next)
	handler.ServeHTTP(rec2, req)

	if rec2.Code != 422 {
		t.Errorf("Code returned = %d, expected %d", rec2.Code, 422)
	}

	if count != 1 {
		t.Errorf("Count returned = %d, expected %d", count, 1)
	}
}

func TestHandlerConcurrentRequests(t *testing.T) {
	jsonRequest := []byte(`{order_id:123, "status": "created"}`)

	var execCount atomic.Int32
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		execCount.Add(1)
		time.Sleep(20 * time.Millisecond)
		body, _ := io.ReadAll(r.Body)
		if !bytes.Equal(body, jsonRequest) {
			t.Errorf("Body returned = %08b, requested %08b", body, jsonRequest)
		}
	})

	m := idempo.New(inmem.New(24*time.Hour, 5*time.Minute), idempo.Options{})
	handler := m.Handler(next)

	const N = 50

	var wg sync.WaitGroup
	start := make(chan struct{})
	codes := make([]int, N)

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/charge", bytes.NewBuffer(jsonRequest))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "019e705d-bb1a-7085-9c1b-58a6a14a1aeb")
			<-start
			handler.ServeHTTP(rec, req)
			codes[i] = rec.Code
		}(i)
	}

	close(start)
	wg.Wait()

	if execCount.Load() != 1 {
		t.Errorf("Atomic count returned = %d, expected %d", execCount.Load(), 1)
	}

	for _, c := range codes {
		if c != 200 && c != 201 && c != 409 {
			t.Errorf("Code returned = %d", c)
		}
	}
}

type fakeStore struct {
	claimResult   idempo.ClaimResult
	claimErr      error
	completeErr   error
	abandonErr    error
	completed     bool
	abandonCalled bool
}

var _ idempo.Store = (*fakeStore)(nil)

func (f *fakeStore) Claim(ctx context.Context, key, requestHash, token string) (idempo.ClaimResult, error) {
	return f.claimResult, f.claimErr
}

func (f *fakeStore) Complete(ctx context.Context, key, token string, statusCode int, headers, body []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.completed = true
	return f.completeErr
}

func (f *fakeStore) Abandon(ctx context.Context, key, token string) error {
	f.abandonCalled = true
	return f.abandonErr
}

func TestHandlerStoreClaimErrorFailsClosed(t *testing.T) {
	store := &fakeStore{claimErr: errors.New("store down")}

	count := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
	})

	handler := idempo.New(store, idempo.Options{}).Handler(next)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("{}"))
	req.Header.Set("Idempotency-Key", "019e705d-bb1a-7085-9c1b-58a6a14a1aeb")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != 500 {
		t.Errorf("Code returned = %d, expected %d", rec.Code, 500)
	}

	if count != 0 {
		t.Errorf("Count returned = %d, expected %d", count, 0)
	}
}

func TestHandlerStoreCompleteErrorStillServesClient(t *testing.T) {
	store := &fakeStore{
		claimResult: idempo.ClaimResult{Status: idempo.StatusNew},
		completeErr: errors.New("store down"),
	}

	count := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("ok"))
	})

	handler := idempo.New(store, idempo.Options{}).Handler(next)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("{}"))
	req.Header.Set("Idempotency-Key", "019e705d-bb1a-7085-9c1b-58a6a14a1aeb")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != 201 {
		t.Errorf("Code returned = %d, expected %d", rec.Code, 201)
	}

	if rec.Body.String() != "ok" {
		t.Errorf("Body returned = %s, expected %s", rec.Body.String(), "ok")
	}

	if count != 1 {
		t.Errorf("Count returned = %d, expected %d", count, 1)
	}
}

func TestHandlerExposesFlusher(t *testing.T) {
	flushed := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("ResponseWriter does not implement http.Flusher")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("chunk"))
		f.Flush()
		flushed = true
	})

	handler := idempo.New(inmem.New(24*time.Hour, 5*time.Minute), idempo.Options{}).Handler(next)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("{}"))
	req.Header.Set("Idempotency-Key", "019e705d-bb1a-7085-9c1b-58a6a14a1aeb")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !flushed {
		t.Error("handler did not flush: http.Flusher was not exposed through the middleware")
	}
}

// hijackableRecorder is an httptest.ResponseRecorder that also implements
// http.Hijacker, so the middleware can forward Hijack to it in tests.
type hijackableRecorder struct {
	*httptest.ResponseRecorder
}

func (h *hijackableRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, nil
}

func TestHandlerHijackAbandonsClaim(t *testing.T) {
	store := &fakeStore{claimResult: idempo.ClaimResult{Status: idempo.StatusNew}}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("ResponseWriter does not implement http.Hijacker")
		}
		hj.Hijack()
	})

	handler := idempo.New(store, idempo.Options{}).Handler(next)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("{}"))
	req.Header.Set("Idempotency-Key", "019e705d-bb1a-7085-9c1b-58a6a14a1aeb")
	rec := &hijackableRecorder{httptest.NewRecorder()}

	handler.ServeHTTP(rec, req)

	if !store.abandonCalled {
		t.Error("expected the claim to be abandoned after a hijack")
	}

	if store.completed {
		t.Error("expected no Complete after a hijack")
	}
}

func TestHandlerOversizedResponseAbandonsClaim(t *testing.T) {
	store := &fakeStore{claimResult: idempo.ClaimResult{Status: idempo.StatusNew}}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(bytes.Repeat([]byte("x"), 100))
	})

	handler := idempo.New(store, idempo.Options{MaxResponseBytes: 10}).Handler(next)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("{}"))
	req.Header.Set("Idempotency-Key", "019e705d-bb1a-7085-9c1b-58a6a14a1aeb")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !store.abandonCalled {
		t.Error("expected the claim to be abandoned for an oversized response")
	}

	if store.completed {
		t.Error("expected no Complete for an oversized response")
	}

	// The cap only limits what is buffered for caching; the client must still
	// receive the full response.
	if rec.Body.Len() != 100 {
		t.Errorf("client body length = %d, expected %d", rec.Body.Len(), 100)
	}
}
