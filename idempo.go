package idempo

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

type Store interface {
	Claim(ctx context.Context, key string, requestHash string) (status string, savedCode int, savedBody []byte, err error)

	Complete(ctx context.Context, key string, statusCode int, body []byte) error
}

type Idempo struct {
	store Store
}

func New(store Store) *Idempo {
	return &Idempo{
		store: store,
	}
}

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.statusCode = code
	sr.ResponseWriter.WriteHeader(code)
}

// RFC 9457 compliant problem details
type problemDetails struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance"`
}

func (m *Idempo) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idemKey := r.Header.Get("Idempotency-Key")

		if len(idemKey) == 0 {
			next.ServeHTTP(w, r)
			return
		}

		recorder := &statusRecorder{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		if !isValidUUIDv7(idemKey) {
			recorder.Header().Set("Content-Type", "application/problem+json")
			recorder.WriteHeader(http.StatusBadRequest)

			pd := new(problemDetails)
			pd.Type = "https://demo.com/errors/bad-request"
			pd.Title = "Invalid Idempotency Key"
			pd.Status = 400
			pd.Detail = "The Idempotency-Key header was provided but does not conform to the required UUIDv7 format."
			pd.Instance = r.URL.Path

			err := json.NewEncoder(recorder.ResponseWriter).Encode(pd)

			if err != nil {
				log.Print(err)
			}

			return
		}

		next.ServeHTTP(recorder, r)
	})
}

func isHex(b byte) bool {
	return ('0' <= b && b <= '9') ||
		('a' <= b && b <= 'f') ||
		('A' <= b && b <= 'F')
}

func isValidUUIDv7(s string) bool {
	if len(s) != 36 {
		return false
	}

	if s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return false
	}

	for i := 0; i < len(s); i++ {
		c := s[i]

		switch i {
		case 8, 13, 18, 23:
			continue
		default:
			if !isHex(c) {
				return false
			}
		}
	}

	if s[14] != '7' {
		return false
	}

	switch strings.ToLower(string(s[19])) {
	case "8", "9", "a", "b":
	default:
		return false
	}

	return true
}
