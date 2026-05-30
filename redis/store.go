package redis

import (
	"context"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

type RedisStore struct {
	client *goredis.Client
	ttl    time.Duration
}

func (rs *RedisStore) Complete(ctx context.Context, key string, statusCode int, body []byte) error {
	hashSetObj := rs.client.HSet(ctx, key, "state", "completed", "responseCode", statusCode, "responseBody", body)

	if hashSetObj.Err() != nil {
		return hashSetObj.Err()
	}

	expireObj := rs.client.Expire(ctx, key, rs.ttl)

	if expireObj.Err() != nil {
		return expireObj.Err()
	}

	return nil
}

func New(opt *goredis.Options, expireDuration time.Duration) *RedisStore {
	return &RedisStore{
		client: goredis.NewClient(opt),
		ttl:    expireDuration,
	}
}
