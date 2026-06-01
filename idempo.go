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
	"time"

	"github.com/google/uuid"
)

type Store interface {
	Claim(ctx context.Context, key string, requestHash string, token string) (status string, savedCode int, savedHeaders []byte, savedBody []byte, err error)

	Complete(ctx context.Context, key string, token string, statusCode int, headers []byte, body []byte) error

	Abandon(ctx context.Context, key string, token string) error
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
	statusCode  int
	body        []byte
	header      http.Header
	wroteHeader bool
}

func (sr *responseRecorder) WriteHeader(code int) {
	sr.statusCode = code
	sr.ResponseWriter.WriteHeader(code)
	sr.header = sr.ResponseWriter.Header().Clone()
	sr.wroteHeader = true
}

func (sr *responseRecorder) Write(body []byte) (int, error) {
	if !sr.wroteHeader {
		sr.WriteHeader(http.StatusOK)
	}

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

		token := uuid.NewString()
		status, savedCode, savedHeaders, savedBody, err := m.store.Claim(r.Context(), idemKey, bodyHash, token)

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
			var header http.Header
			json.Unmarshal(savedHeaders, &header)

			for k, v := range header {
				recorder.Header()[k] = v
			}

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

		persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		defer func() {
			rec := recover()
			if rec != nil {
				_ = m.store.Abandon(persistCtx, idemKey, token)
				panic(rec)
			}
		}()

		next.ServeHTTP(recorder, r)

		if recorder.statusCode >= 500 {
			err = m.store.Abandon(persistCtx, idemKey, token)
		} else {
			headerBytes, marshalErr := json.Marshal(recorder.header)
			if marshalErr != nil {
				log.Print(marshalErr)
			}
			err = m.store.Complete(persistCtx, idemKey, token, recorder.statusCode, headerBytes, recorder.body)
		}

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
