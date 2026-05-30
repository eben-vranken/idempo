package inmem

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

type Entry struct {
	bodyHash     string
	state        string
	responseCode int
	responseBody []byte
	expiryTime   time.Time
}

type InMemStore struct {
	keys map[string]*Entry
	m    sync.Mutex
	ttl  time.Duration
	done chan struct{}
}

func (ims *InMemStore) Close() {
	close(ims.done)
}

func (ims *InMemStore) Claim(ctx context.Context, key string, requestHash string) (string, int, []byte, error) {
	ims.m.Lock()
	defer ims.m.Unlock()

	val, ok := ims.keys[key]

	// Claim new key
	if !ok {
		ims.keys[key] = &Entry{
			bodyHash:   requestHash,
			state:      "pending",
			expiryTime: time.Now().Add(ims.ttl),
		}

		return "new", 0, nil, nil
	}

	// Key exists but expired
	if val.expiryTime.Before(time.Now()) {
		ims.keys[key] = &Entry{
			bodyHash:   requestHash,
			state:      "pending",
			expiryTime: time.Now().Add(ims.ttl),
		}

		return "new", 0, nil, nil
	}

	// Key exists, state is pending
	if val.state == "pending" {
		return val.state, val.responseCode, nil, nil
	}

	// Key exists, body is different
	if val.state == "completed" && val.bodyHash != requestHash {
		return "conflict", http.StatusUnprocessableEntity, val.responseBody, nil
	}

	// Key exists, state is completed
	if val.state == "completed" {
		return val.state, val.responseCode, val.responseBody, nil
	}

	return val.state, val.responseCode, val.responseBody, nil
}

func (ims *InMemStore) Complete(ctx context.Context, key string, statusCode int, body []byte) error {
	ims.m.Lock()
	defer ims.m.Unlock()

	val, ok := ims.keys[key]

	if !ok {
		return errors.New("key was not found")
	}

	val.state = "completed"
	val.responseCode = statusCode
	val.responseBody = body

	return nil
}

func New(expireDuration time.Duration) *InMemStore {
	ims := new(InMemStore)
	ims.keys = make(map[string]*Entry)
	ims.m = sync.Mutex{}
	ims.ttl = expireDuration
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
