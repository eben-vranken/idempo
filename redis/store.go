package redis

import (
	"context"
	"strconv"
	"time"

	"github.com/eben-vranken/idempo"
	goredis "github.com/redis/go-redis/v9"
)

var _ idempo.Store = (*RedisStore)(nil)

var claimScript = `
	-- ARGV: 1=bodyHash, 2=ttlSeconds, 3=token
	-- return table: {status, responseCode, responseHeaders, responseBody}
	local data = redis.call('HGETALL', KEYS[1])
	if #data == 0 then
		redis.call('HSET', KEYS[1], 'state', 'pending', 'bodyHash', ARGV[1], 'token', ARGV[3])
		redis.call('EXPIRE', KEYS[1], ARGV[2])
		return {'new', '', '', ''}
	end
	local state, bodyHash, responseCode, responseHeaders, responseBody = '', '', '', '', ''
	for i = 1, #data, 2 do
		if data[i] == 'state' then state = data[i+1] end
		if data[i] == 'bodyHash' then bodyHash = data[i+1] end
		if data[i] == 'responseCode' then responseCode = data[i+1] end
		if data[i] == 'responseHeaders' then responseHeaders = data[i+1] end
		if data[i] == 'responseBody' then responseBody = data[i+1] end
	end
	if state == 'pending' then return {'pending', '', '', ''} end
	if bodyHash ~= ARGV[1] then return {'conflict', '', '', ''} end
	return {'completed', responseCode, responseHeaders, responseBody}
`

var completeScript = `
	-- ARGV: 1=token, 2=responseCode, 3=responseHeaders, 4=responseBody, 5=ttlSeconds
	local stored = redis.call('HGET', KEYS[1], 'token')
	if stored ~= ARGV[1] then
		return 0          -- not our claim (or key gone): no-op
	end
	if redis.call('HGET', KEYS[1], 'state') ~= 'pending' then
		return 0          -- already completed/reset: no-op
	end
	redis.call('HSET', KEYS[1], 'state', 'completed', 'responseCode', ARGV[2], 'responseHeaders', ARGV[3], 'responseBody', ARGV[4])
	redis.call('EXPIRE', KEYS[1], ARGV[5])
	return 1              -- completed
`

var abandonScript = `
	-- ARGV: 1=token
	if redis.call('HGET', KEYS[1], 'token') == ARGV[1] then
		redis.call('DEL', KEYS[1])
	end
	return 1
`

type RedisStore struct {
	client *goredis.Client
	ttl    time.Duration
}

func (rs *RedisStore) Claim(ctx context.Context, key string, requestHash string, token string) (status string, savedCode int, savedHeaders []byte, savedBody []byte, err error) {
	result, err := rs.client.Eval(ctx, claimScript, []string{key}, requestHash, int(rs.ttl.Seconds()), token).Result()

	if err != nil {
		return "", 0, nil, nil, err
	}

	res := result.([]interface{})

	status = res[0].(string)
	responseCodeString := res[1].(string)
	responseHeaders := res[2].(string)
	responseBody := res[3].(string)

	var responseCode int
	if responseCodeString != "" {
		responseCode, err = strconv.Atoi(responseCodeString)
		if err != nil {
			return "", 0, nil, nil, err
		}
	}

	return status, responseCode, []byte(responseHeaders), []byte(responseBody), nil
}

func (rs *RedisStore) Complete(ctx context.Context, key string, token string, statusCode int, headers []byte, body []byte) error {
	_, err := rs.client.Eval(ctx, completeScript, []string{key}, token, statusCode, headers, body, int(rs.ttl.Seconds())).Result()

	if err != nil {
		return err
	}

	return nil
}

func (rs *RedisStore) Abandon(ctx context.Context, key string, token string) error {
	_, err := rs.client.Eval(ctx, abandonScript, []string{key}, token).Result()

	if err != nil {
		return err
	}

	return nil
}

func New(opt *goredis.Options, expireDuration time.Duration) *RedisStore {
	return &RedisStore{
		client: goredis.NewClient(opt),
		ttl:    expireDuration,
	}
}
