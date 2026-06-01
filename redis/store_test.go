package redis_test

import (
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

	status, _, _, _ := store.Claim(context.Background(), key, requestHash)

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
