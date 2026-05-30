package idempo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
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

type responseRecorder struct {
	http.ResponseWriter
	statusCode int
	body       []byte
}

func (sr *responseRecorder) WriteHeader(code int) {
	sr.statusCode = code
	sr.ResponseWriter.WriteHeader(code)
}

func (sr *responseRecorder) Write(body []byte) (int, error) {
	sr.body = append(sr.body, body...)
	return sr.ResponseWriter.Write(body)
}

// RFC 9457 compliant problem details
type problemDetails struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance"`
}

func writeProblem(status int, problemType string, title string, detail string, instance string) problemDetails {
	return problemDetails{
		Status:   status,
		Type:     problemType,
		Title:    title,
		Detail:   detail,
		Instance: instance,
	}
}

func (m *Idempo) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idemKey := r.Header.Get("Idempotency-Key")

		if len(idemKey) == 0 {
			next.ServeHTTP(w, r)
			return
		}

		recorder := &responseRecorder{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		if !isValidUUIDv7(idemKey) {
			recorder.Header().Set("Content-Type", "application/problem+json")
			recorder.WriteHeader(http.StatusBadRequest)

			pd := writeProblem(400, "https://demo.com/errors/bad-request", "Invalid Idempotency Key", "The Idempotency-Key header was provided but does not conform to the required UUIDv7 format.", r.URL.Path)

			err := json.NewEncoder(recorder.ResponseWriter).Encode(pd)

			if err != nil {
				log.Print(err)
			}

			return
		}

		body, err := io.ReadAll(r.Body)

		if err != nil {
			recorder.Header().Set("Content-Type", "application/problem+json")
			recorder.WriteHeader(http.StatusInternalServerError)

			pd := writeProblem(500, "https://demo.com/errors/internal-server-error", "Internal Server Error", "Our server failed parsing the request body.", r.URL.Path)

			err := json.NewEncoder(recorder.ResponseWriter).Encode(pd)

			if err != nil {
				log.Print(err)
			}

			return
		}

		reader := bytes.NewReader(body)
		r.Body = io.NopCloser(reader)

		bodyHash := fmt.Sprintf("%x", sha256.Sum256(body))

		status, savedCode, savedBody, err := m.store.Claim(r.Context(), idemKey, bodyHash)

		if err != nil {
			recorder.Header().Set("Content-Type", "application/problem+json")
			recorder.WriteHeader(http.StatusInternalServerError)

			pd := writeProblem(500, "https://demo.com/errors/internal-server-error", "Internal Server Error", "Our server failed parsing the request body.", r.URL.Path)

			err := json.NewEncoder(recorder.ResponseWriter).Encode(pd)

			if err != nil {
				log.Print(err)
			}

			return
		}

		if status == "completed" {
			recorder.Header().Set("Content-Type", "application/json")
			recorder.Header().Set("Idempotency-Replayed", "true")
			recorder.WriteHeader(savedCode)
			recorder.Write(savedBody)
			return
		}

		if status == "pending" {
			recorder.Header().Set("Content-Type", "application/problem+json")
			recorder.WriteHeader(http.StatusConflict)

			pd := writeProblem(409, "https://demo.com/errors/conflict", "Status Conflict", "Another request is already handing this request.", r.URL.Path)

			err := json.NewEncoder(recorder.ResponseWriter).Encode(pd)

			if err != nil {
				log.Print(err)
			}
			return
		}

		if status == "conflict" {
			recorder.Header().Set("Content-Type", "application/problem+json")
			recorder.WriteHeader(http.StatusUnprocessableEntity)

			pd := writeProblem(422, "https://demo.com/errors/body-mismatch", "Unprocessable Entity", "A request with this key but different body has hit this server already.", r.URL.Path)

			err := json.NewEncoder(recorder.ResponseWriter).Encode(pd)

			if err != nil {
				log.Print(err)
			}

			return
		}

		next.ServeHTTP(recorder, r)

		err = m.store.Complete(r.Context(), idemKey, recorder.statusCode, recorder.body)

		if err != nil {
			log.Print(err)
		}
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
