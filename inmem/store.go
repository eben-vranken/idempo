package inmem

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/eben-vranken/idempo"
)

var _ idempo.Store = (*InMemStore)(nil)

type Entry struct {
	token           string
	bodyHash        string
	state           string
	responseCode    int
	responseHeaders []byte
	responseBody    []byte
	expiryTime      time.Time
}

type InMemStore struct {
	keys         map[string]*Entry
	m            sync.Mutex
	lockTTL      time.Duration
	retentionTTL time.Duration
	closeOnce    sync.Once

	done chan struct{}
}

func (ims *InMemStore) Close() {
	ims.closeOnce.Do(func() {
		close(ims.done)
	})
}

func (ims *InMemStore) Claim(ctx context.Context, key string, requestHash string, token string) (string, int, []byte, []byte, error) {
	ims.m.Lock()
	defer ims.m.Unlock()

	val, ok := ims.keys[key]

	// Claim new key
	if !ok {
		ims.keys[key] = &Entry{
			token:      token,
			bodyHash:   requestHash,
			state:      "pending",
			expiryTime: time.Now().Add(ims.lockTTL),
		}

		return "new", 0, nil, nil, nil
	}

	// Key exists but expired
	if val.expiryTime.Before(time.Now()) {
		ims.keys[key] = &Entry{
			token:      token,
			bodyHash:   requestHash,
			state:      "pending",
			expiryTime: time.Now().Add(ims.lockTTL),
		}

		return "new", 0, nil, nil, nil
	}

	// Key exists, state is pending
	if val.state == "pending" {
		return val.state, val.responseCode, nil, nil, nil
	}

	// Key exists, body is different
	if val.state == "completed" && val.bodyHash != requestHash {
		return "conflict", http.StatusUnprocessableEntity, nil, val.responseBody, nil
	}

	// Key exists, state is completed
	if val.state == "completed" {
		return val.state, val.responseCode, val.responseHeaders, val.responseBody, nil
	}

	return val.state, val.responseCode, nil, val.responseBody, nil
}

func (ims *InMemStore) Complete(ctx context.Context, key string, token string, statusCode int, header []byte, body []byte) error {
	ims.m.Lock()
	defer ims.m.Unlock()

	val, ok := ims.keys[key]

	if !ok {
		return errors.New("key was not found")
	}

	if val.token != token {
		return nil
	}

	val.state = "completed"
	val.responseCode = statusCode
	val.responseHeaders = header
	val.responseBody = body
	val.expiryTime = time.Now().Add(ims.retentionTTL)

	return nil
}

func (ims *InMemStore) Abandon(ctx context.Context, key string, token string) error {
	ims.m.Lock()
	defer ims.m.Unlock()

	val, ok := ims.keys[key]

	if !ok {
		return nil
	}

	if val.token != token {
		return nil
	}

	delete(ims.keys, key)

	return nil
}

func New(lockTTL time.Duration, retentionTTL time.Duration) *InMemStore {
	ims := new(InMemStore)
	ims.keys = make(map[string]*Entry)
	ims.m = sync.Mutex{}
	ims.lockTTL = lockTTL
	ims.retentionTTL = retentionTTL
	ims.done = make(chan struct{})

	ticker := time.NewTicker(60 * time.Second)
	go func() {
		for {
			select {
			case <-ticker.C:
				clearExpiredKeys(ims)
			case <-ims.done:
				ticker.Stop()
				return
			}
		}
	}()

	return ims
}

func clearExpiredKeys(ims *InMemStore) {
	ims.m.Lock()
	defer ims.m.Unlock()

	for key, entry := range ims.keys {
		if entry.expiryTime.Before(time.Now()) {
			delete(ims.keys, key)
		}
	}
}
