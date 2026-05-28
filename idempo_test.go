package idempo_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/eben-vranken/idempo"
)

func TestHandler(t *testing.T) {
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

			// TO-DO: Use a real store once Handler uses it.
			m := idempo.New(nil)
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
