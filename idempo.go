package idempo

import (
	"context"
	"net/http"
)

type Store interface {
	Claim(ctx context.Context, key string, requestHash string) (status string, savedCode int, savedBody []byte, err error)

	Complete(ctx context.Context, key string, statusCode int, body []byte) error
}

type Middleware struct {
	store Store
}

func New(store Store) *Middleware {
	return &Middleware{
		store: store,
	}
}

func (m *Middleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}
