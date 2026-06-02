// Package idempo provides framework-agnostic HTTP middleware that makes unsafe
// requests idempotent using the IETF Idempotency-Key header.
//
// A client supplies an Idempotency-Key header. The middleware records the first
// response produced for that key and replays it for any later request that
// reuses the key, so a retried request runs its side effect at most once.
// Storage is pluggable through the Store interface, with in-memory, Redis, and
// Postgres backends provided in subpackages.
package idempo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// ClaimStatus is the outcome of a Claim, reported in ClaimResult.Status.
type ClaimStatus string

const (
	// StatusNew means the caller won the claim and now owns the key. It must
	// eventually call Complete or Abandon with the same token.
	StatusNew ClaimStatus = "new"
	// StatusPending means another in-flight request already holds the key, so
	// the caller should not run the handler.
	StatusPending ClaimStatus = "pending"
	// StatusCompleted means a previous request already finished. The stored
	// response to replay is carried in ClaimResult.Code, Headers, and Body.
	StatusCompleted ClaimStatus = "completed"
	// StatusConflict means the key exists but the request fingerprint differs
	// from the stored one, so the same key was reused for a different request.
	StatusConflict ClaimStatus = "conflict"
)

// Store persists idempotency records keyed by the idempotency key and backs the
// middleware with a pluggable storage backend.
//
// Implementations must be safe for concurrent use by multiple goroutines: the
// middleware calls Store from many request handlers at once. Each record
// follows a lifecycle of a single Claim followed by exactly one Complete or
// Abandon, matched by a caller-generated fencing token.
type Store interface {
	// Claim atomically attempts to reserve key for the current request.
	//
	// The atomicity guarantee is the core of the middleware: when many requests
	// claim the same key at once, exactly one of them receives StatusNew.
	//
	// requestHash is an opaque fingerprint of the request; the store only
	// compares it and never interprets it. token is a unique, caller-generated
	// fencing token for this attempt; the store must persist it so that Complete
	// and Abandon can later verify ownership.
	//
	// The returned ClaimResult.Status is one of:
	//   - StatusNew: the caller won and now owns the key, held pending under the
	//     lock TTL. It must later call Complete or Abandon with the same token.
	//   - StatusPending: another request already holds an in-flight claim.
	//   - StatusCompleted: a previous request finished; the response to replay is
	//     in ClaimResult.Code, Headers, and Body.
	//   - StatusConflict: the key exists but requestHash differs from the stored
	//     fingerprint.
	//
	// An expired pending or completed record may be reclaimed as StatusNew.
	// Claim must honor ctx cancellation and deadlines.
	Claim(ctx context.Context, key string, requestHash string, token string) (ClaimResult, error)

	// Complete records the final response for a finished request and moves the
	// record to a completed state, retained under the retention TTL.
	//
	// Complete must be a no-op if the stored token does not match token, or if
	// the record is not pending. This fencing rule prevents a slow request from
	// overwriting a claim that a newer request now owns. Complete must honor ctx
	// cancellation and deadlines.
	Complete(ctx context.Context, key string, token string, statusCode int, headers []byte, body []byte) error

	// Abandon releases a claim so the key can be retried, for example after the
	// handler fails, returns a 5xx, or panics.
	//
	// Abandon must be a no-op if the stored token does not match token, so a late
	// call cannot release a claim that a newer request now owns. Abandon must
	// honor ctx cancellation and deadlines.
	Abandon(ctx context.Context, key string, token string) error
}

type Idempo struct {
	store             Store
	maxBodyBytes      int64
	PersistentTimeout time.Duration
	logger            slog.Logger
}

// ClaimResult is the outcome of a Claim.
//
// Code, Headers, and Body carry the stored response to replay and are populated
// only when Status is StatusCompleted; otherwise they are zero.
type ClaimResult struct {
	// Status is the claim outcome and is always set.
	Status ClaimStatus
	// Code is the stored response status code.
	Code int
	// Headers is the stored response header set, exactly as written to Complete.
	Headers []byte
	// Body is the stored response body, exactly as written to Complete.
	Body []byte
}

func New(store Store, opts Options) *Idempo {
	if opts.MaxBodyBytes == 0 {
		opts.MaxBodyBytes = defaultMaxBodyBytes
	}
	if opts.PersistentTimeout == 0 {
		opts.PersistentTimeout = defaultPersistentTImeout
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Idempo{
		store:             store,
		maxBodyBytes:      opts.MaxBodyBytes,
		PersistentTimeout: opts.PersistentTimeout,
		logger:            *opts.Logger,
	}
}

type Options struct {
	MaxBodyBytes      int64
	PersistentTimeout time.Duration
	Logger            *slog.Logger
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

// writeProblem sends an RFC 9457 problem+json response with the given status
// and fields. It logs at Warn level if the response body cannot be written,
// which usually means the client disconnected mid-response.
func (m *Idempo) writeProblem(rec *responseRecorder, status int, problemType, title, detail, instance string) {
	rec.Header().Set("Content-Type", "application/problem+json")
	rec.WriteHeader(status)

	pd := problemDetails{
		Status:   status,
		Type:     problemType,
		Title:    title,
		Detail:   detail,
		Instance: instance,
	}

	if err := json.NewEncoder(rec.ResponseWriter).Encode(pd); err != nil {
		m.logger.Warn("idempo: failed to write problem response", "err", err)
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
			m.writeProblem(recorder, http.StatusBadRequest, "https://demo.com/errors/key-too-long", "Invalid Idempotency Key", "The Idempotency-Key header exceeds the maximum length of 255 characters", r.URL.Path)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, m.maxBodyBytes)
		body, err := io.ReadAll(r.Body)

		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				m.writeProblem(recorder, http.StatusRequestEntityTooLarge, "https://demo.com/errors/content-too-large", "Content Too Large", "Body request body size was too large", r.URL.Path)
			} else {
				m.writeProblem(recorder, http.StatusInternalServerError, "https://demo.com/errors/internal-server-error", "Internal Server Error", "Our server idempotency store is unavailable.", r.URL.Path)
			}
			return
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
		result, err := m.store.Claim(r.Context(), idemKey, bodyHash, token)

		if err != nil {
			m.writeProblem(recorder, http.StatusInternalServerError, "https://demo.com/errors/internal-server-error", "Internal Server Error", "Our server failed parsing the request body.", r.URL.Path)
			return
		}

		if result.Status == StatusCompleted {
			var header http.Header
			json.Unmarshal(result.Headers, &header)

			for k, v := range header {
				recorder.Header()[k] = v
			}

			recorder.Header().Set("Idempotency-Replayed", "true")
			recorder.WriteHeader(result.Code)
			recorder.Write(result.Body)
			return
		}

		if result.Status == StatusPending {
			m.writeProblem(recorder, http.StatusConflict, "https://demo.com/errors/conflict", "Status Conflict", "Another request is already handing this request.", r.URL.Path)
			return
		}

		if result.Status == StatusConflict {
			m.writeProblem(recorder, http.StatusUnprocessableEntity, "https://demo.com/errors/body-mismatch", "Unprocessable Entity", "A request with this key but different body has hit this server already.", r.URL.Path)
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
				m.logger.Error("idempo: failed to marshal response headers", "err", marshalErr)
			}
			err = m.store.Complete(persistCtx, idemKey, token, recorder.statusCode, headerBytes, recorder.body)
		}

		if err != nil {
			m.logger.Error("idempo: failed to persist result", "err", err, "key", idemKey)
		}
	})
}
