package redis_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/eben-vranken/idempo/redis"
	goredis "github.com/redis/go-redis/v9"
)

func TestClaimNewKey(t *testing.T) {
	_, store := newTestStore(t)

	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"

	status, _, _, _ := store.Claim(context.Background(), key, requestHash, "token")

	if status != "new" {
		t.Errorf("Status returned = %s, requested %s", status, "new")
	}
}

func TestClaimReturnsPending(t *testing.T) {
	_, store := newTestStore(t)

	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"

	_, _, _, _ = store.Claim(context.Background(), key, requestHash, "token")

	status, _, _, _ := store.Claim(context.Background(), key, requestHash, "token")

	if status != "pending" {
		t.Errorf("Status returned = %s, requested %s", status, "pending")
	}
}

func TestClaimConflictedKey(t *testing.T) {
	_, store := newTestStore(t)

	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"

	_, statusCode, savedBody, _ := store.Claim(context.Background(), key, requestHash, "token")

	err := store.Complete(context.Background(), key, "token", statusCode, savedBody)

	if err != nil {
		t.Fatal(err)
	}

	differentRequestHash := "81fd8e12b33d548c41873494bb73c2c6b841b157ce0860857e70f41af9f24337"
	status, _, _, _ := store.Claim(context.Background(), key, differentRequestHash, "token")

	if status != "conflict" {
		t.Errorf("Status returned = %s, requested %s", status, "conflict")
	}
}

func TestClaimCompletedKey(t *testing.T) {
	_, store := newTestStore(t)

	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"

	_, _, _, _ = store.Claim(context.Background(), key, requestHash, "token")

	wantBody := []byte(`{"ok": "true"}`)
	err := store.Complete(context.Background(), key, "token", 201, wantBody)

	if err != nil {
		t.Fatal(err)
	}

	status, statusCode, savedBody, _ := store.Claim(context.Background(), key, requestHash, "token")

	if status != "completed" {
		t.Errorf("Status returned = %s, requested %s", status, "completed")
	}

	if statusCode != 201 {
		t.Errorf("Status code returned = %d, requested %d", statusCode, 201)
	}

	if !bytes.Equal(savedBody, wantBody) {
		t.Errorf("Respone body returned = %s, requested %s", savedBody, wantBody)
	}
}

func TestExpiredTTL(t *testing.T) {
	mr, store := newTestStore(t)

	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"

	_, _, _, _ = store.Claim(context.Background(), key, requestHash, "token")

	mr.FastForward(25 * time.Hour)

	status, _, _, _ := store.Claim(context.Background(), key, requestHash, "token")

	if status != "new" {
		t.Errorf("Status returned = %s, requested %s", status, "new")
	}
}

func newTestStore(t *testing.T) (*miniredis.Miniredis, *redis.RedisStore) {
	mr, err := miniredis.Run()

	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { mr.Close() })

	return mr, redis.New(&goredis.Options{Addr: mr.Addr()}, 24*time.Hour)
}
