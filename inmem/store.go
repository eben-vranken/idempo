package inmem

import (
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
	keys map[string]Entry
	m    sync.Mutex
}

func New() *InMemStore {
	return &InMemStore{
		keys: make(map[string]Entry),
		m:    sync.Mutex{},
	}
}
