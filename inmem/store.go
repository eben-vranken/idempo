package inmem

import (
	"context"
	"errors"
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
			expiryTime: time.Now().Add(time.Hour * 24),
		}

		return "new", 0, nil, nil
	}

	// Key exists but expired
	if val.expiryTime.Before(time.Now()) {
		ims.keys[key] = &Entry{
			bodyHash:   requestHash,
			state:      "pending",
			expiryTime: time.Now().Add(time.Hour * 24),
		}

		return "new", 0, nil, nil
	}

	// Key exists, state is pending
	if val.state == "pending" {
		return val.state, val.responseCode, nil, nil
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

func New() *InMemStore {
	return &InMemStore{
		keys: make(map[string]*Entry),
		m:    sync.Mutex{},
	}
}
