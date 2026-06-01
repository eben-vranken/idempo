package idempo_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/eben-vranken/idempo"
	"github.com/eben-vranken/idempo/inmem"
)

func TestUUIDv7IsValid(t *testing.T) {
	cases := []struct {
		name       string
		key        string
		wantCode   int
		nextCalled bool
	}{
		{
			name:       "UUID too short",
			key:        "abc",
			wantCode:   400,
			nextCalled: false,
		},
		{
			name:       "Valid UUIDv4 with version nibble",
			key:        "123e4567-e89b-42d3-a456-426614174000",
			wantCode:   400,
			nextCalled: false,
		},
		{
			name:       "Bad variant nibble",
			key:        "123e4567-e89b-72d3-7456-426614174000",
			wantCode:   400,
			nextCalled: false,
		},
		{
			name:       "Non-hex character injected",
			key:        "123e4567-e89b-72d3-a456-42661417z000",
			wantCode:   400,
			nextCalled: false,
		},
		{
			name:       "Wrong hyphen position",
			key:        "123e4567e-89b7-2d3a-8456-426614174000",
			wantCode:   400,
			nextCalled: false,
		},
		{
			name:       "Uppercase hex",
			key:        "123E4567-E89B-72D3-A456-426614174000",
			wantCode:   200,
			nextCalled: true,
		},
		{
			name:       "Valid UUIDv7",
			key:        "019e705d-bb1a-7085-9c1b-58a6a14a1aeb",
			wantCode:   200,
			nextCalled: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
			})

			m := idempo.New(inmem.New(24 * time.Hour))
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

	m := idempo.New(inmem.New(24 * time.Hour))
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

	m := idempo.New(inmem.New(24 * time.Hour))
	handler := m.Handler(next)
	handler.ServeHTTP(rec, req)

	req = httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(jsonRequest))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "019e705d-bb1a-7085-9c1b-58a6a14a1aeb")
	rec2 := httptest.NewRecorder()

	handler = m.Handler(next)
	handler.ServeHTTP(rec2, req)

	if rec2.Code != 201 {
		t.Errorf("Code returned = %08b, expected %08b", rec2.Code, 201)
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

	m := idempo.New(inmem.New(24 * time.Hour))
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
		t.Errorf("Code returned = %08b, expected %08b", rec2.Code, 422)
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

	m := idempo.New(inmem.New(1 * time.Millisecond))
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

	m := idempo.New(inmem.New(24 * time.Hour))
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

	m := idempo.New(inmem.New(24 * time.Hour))
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

	m := idempo.New(inmem.New(24 * time.Hour))
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
