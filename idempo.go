package idempo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
)

type Store interface {
	Claim(ctx context.Context, key string, requestHash string, token string) (status string, savedCode int, savedHeaders []byte, savedBody []byte, err error)

	Complete(ctx context.Context, key string, token string, statusCode int, headers []byte, body []byte) error

	Abandon(ctx context.Context, key string, token string) error
}

type Idempo struct {
	store             Store
	maxBodyBytes      int64
	PersistentTimeout time.Duration
}

func New(store Store, opts Options) *Idempo {
	if opts.MaxBodyBytes == 0 {
		opts.MaxBodyBytes = defaultMaxBodyBytes
	}
	if opts.PersistentTimeout == 0 {
		opts.PersistentTimeout = defaultPersistentTImeout
	}
	return &Idempo{
		store:             store,
		maxBodyBytes:      opts.MaxBodyBytes,
		PersistentTimeout: opts.PersistentTimeout,
	}
}

type Options struct {
	MaxBodyBytes      int64
	PersistentTimeout time.Duration
}

const defaultMaxBodyBytes = 1 << 20
const defaultPersistentTImeout = time.Second * 10
const maxKeyLen = 255

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

		if len(idemKey) > maxKeyLen {
			recorder.Header().Set("Content-Type", "application/problem+json")
			recorder.WriteHeader(http.StatusBadRequest)

			pd := writeProblem(400, "https://demo.com/errors/key-too-long", "Invalid Idempotency Key", "The Idempotency-Key header exceeds the maximum length of 255 characters", r.URL.Path)

			err := json.NewEncoder(recorder.ResponseWriter).Encode(pd)

			if err != nil {
				log.Print(err)
			}

			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, m.maxBodyBytes)
		body, err := io.ReadAll(r.Body)

		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				recorder.Header().Set("Content-Type", "application/problem+json")
				recorder.WriteHeader(http.StatusRequestEntityTooLarge)

				pd := writeProblem(413, "https://demo.com/errors/content-too-large", "Content Too Large", "Body request body size was too large", r.URL.Path)

				err := json.NewEncoder(recorder.ResponseWriter).Encode(pd)

				if err != nil {
					log.Print(err)
				}

				return
			} else {
				recorder.Header().Set("Content-Type", "application/problem+json")
				recorder.WriteHeader(http.StatusInternalServerError)

				pd := writeProblem(500, "https://demo.com/errors/internal-server-error", "Internal Server Error", "Our server idempotency store is unavailable.", r.URL.Path)

				err := json.NewEncoder(recorder.ResponseWriter).Encode(pd)

				if err != nil {
					log.Print(err)
				}

				return
			}
		}

		reader := bytes.NewReader(body)
		r.Body = io.NopCloser(reader)

		h := sha256.New()
		io.WriteString(h, r.Method)
		io.WriteString(h, "\n")
		io.WriteString(h, r.URL.Path)
		io.WriteString(h, "\n")
		h.Write(body)
		sum := h.Sum(nil)
		bodyHash := hex.EncodeToString(sum)

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

		defer func() {
			rec := recover()
			if rec != nil {
				abandonCtx, cancel := context.WithTimeout(context.Background(), m.PersistentTimeout)
				_ = m.store.Abandon(abandonCtx, idemKey, token)
				cancel()
				panic(rec)
			}
		}()

		next.ServeHTTP(recorder, r)
		persistCtx, cancel := context.WithTimeout(context.Background(), m.PersistentTimeout)

		defer cancel()

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
