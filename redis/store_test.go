package redis_test

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/eben-vranken/idempo/redis"
	goredis "github.com/redis/go-redis/v9"
)

func TestClaimNewKey(t *testing.T) {
	_, store := newTestStore(t, time.Hour*24, time.Minute*5)

	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"

	status, _, _, _, _ := store.Claim(context.Background(), key, requestHash, "token")

	if status != "new" {
		t.Errorf("Status returned = %s, requested %s", status, "new")
	}
}

func TestClaimReturnsPending(t *testing.T) {
	_, store := newTestStore(t, time.Hour*24, time.Minute*5)

	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"

	_, _, _, _, _ = store.Claim(context.Background(), key, requestHash, "token")

	status, _, _, _, _ := store.Claim(context.Background(), key, requestHash, "token")

	if status != "pending" {
		t.Errorf("Status returned = %s, requested %s", status, "pending")
	}
}

func TestClaimConflictedKey(t *testing.T) {
	_, store := newTestStore(t, time.Hour*24, time.Minute*5)

	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"

	_, statusCode, savedHeader, savedBody, _ := store.Claim(context.Background(), key, requestHash, "token")

	err := store.Complete(context.Background(), key, "token", statusCode, savedHeader, savedBody)

	if err != nil {
		t.Fatal(err)
	}

	differentRequestHash := "81fd8e12b33d548c41873494bb73c2c6b841b157ce0860857e70f41af9f24337"
	status, _, _, _, _ := store.Claim(context.Background(), key, differentRequestHash, "token")

	if status != "conflict" {
		t.Errorf("Status returned = %s, requested %s", status, "conflict")
	}
}

func TestClaimCompletedKey(t *testing.T) {
	_, store := newTestStore(t, time.Hour*24, time.Minute*5)

	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"

	_, _, _, _, _ = store.Claim(context.Background(), key, requestHash, "token")

	wantBody := []byte(`{"ok": "true"}`)
	headerBytes := []byte(`{"Content-Type":["text/plain"]}`)
	err := store.Complete(context.Background(), key, "token", 201, headerBytes, wantBody)

	if err != nil {
		t.Fatal(err)
	}

	status, statusCode, savedHeaders, savedBody, _ := store.Claim(context.Background(), key, requestHash, "token")

	if status != "completed" {
		t.Errorf("Status returned = %s, requested %s", status, "completed")
	}

	if statusCode != 201 {
		t.Errorf("Status code returned = %d, requested %d", statusCode, 201)
	}

	if !bytes.Equal(savedBody, wantBody) {
		t.Errorf("Respone body returned = %s, requested %s", savedBody, wantBody)
	}

	if !bytes.Equal(savedHeaders, headerBytes) {
		t.Errorf("Header returned = %s, requested %s", savedHeaders, headerBytes)
	}
}

func TestExpiredTTL(t *testing.T) {
	mr, store := newTestStore(t, time.Hour*24, time.Minute*5)

	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"

	_, _, _, _, _ = store.Claim(context.Background(), key, requestHash, "token")

	mr.FastForward(25 * time.Hour)

	status, _, _, _, _ := store.Claim(context.Background(), key, requestHash, "token")

	if status != "new" {
		t.Errorf("Status returned = %s, requested %s", status, "new")
	}
}

func TestClaimConcurrentSingleWinner(t *testing.T) {
	_, store := newTestStore(t, time.Hour*24, time.Minute*5)

	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"
	var newCount atomic.Int32
	const N = 50
	statuses := make([]string, N)
	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := range N {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			token := fmt.Sprintf("token-%d", i)
			<-start
			status, _, _, _, err := store.Claim(context.Background(), key, requestHash, token)

			if err != nil {
				t.Errorf("DB error: %s", err)
			}

			statuses[i] = status

			if status == "new" {
				newCount.Add(1)
			}
		}(i)
	}

	close(start)
	wg.Wait()

	if newCount.Load() != 1 {
		t.Errorf("Atomic count returned = %d, expected %d", newCount.Load(), 1)
	}

	for _, c := range statuses {
		if c != "new" && c != "pending" {
			t.Errorf("Status returned = %s, expected 'new' or 'pending'", c)
		}
	}
}

func newTestStore(t *testing.T, lockTTL time.Duration, retentionTTL time.Duration) (*miniredis.Miniredis, *redis.RedisStore) {
	mr, err := miniredis.Run()

	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { mr.Close() })

	return mr, redis.New(&goredis.Options{Addr: mr.Addr()}, lockTTL, retentionTTL)
}
