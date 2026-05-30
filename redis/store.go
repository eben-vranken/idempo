package redis

import (
	"context"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

var claimScript = `
local data = redis.call('HGETALL', KEYS[1])
if #data == 0 then
    redis.call('HSET', KEYS[1], 'state', 'pending', 'bodyHash', ARGV[1])
    redis.call('EXPIRE', KEYS[1], ARGV[2])
    return {'new', '', '', ''}
end
local state, bodyHash, responseCode, responseBody = '', '', '', ''
for i = 1, #data, 2 do
    if data[i] == 'state' then state = data[i+1] end
    if data[i] == 'bodyHash' then bodyHash = data[i+1] end
    if data[i] == 'responseCode' then responseCode = data[i+1] end
    if data[i] == 'responseBody' then responseBody = data[i+1] end
end
if state == 'pending' then return {'pending', '', '', ''} end
if bodyHash ~= ARGV[1] then return {'conflict', '', '', ''} end
return {'completed', responseCode, responseBody, ''}

`

type RedisStore struct {
	client *goredis.Client
	ttl    time.Duration
}

func (rs *RedisStore) Claim(ctx context.Context, key string, requestHash string) (status string, savedCode int, savedBody []byte, err error) {
	result, err := rs.client.Eval(ctx, claimScript, []string{key}, requestHash, int(rs.ttl.Seconds())).Result()

	if err != nil {
		return "", 0, nil, err
	}

	res := result.([]interface{})

	status = res[0].(string)
	responseCodeString := res[1].(string)
	responseBody := res[2].(string)

	var responseCode int
	if responseCodeString != "" {
		responseCode, err = strconv.Atoi(responseCodeString)
		if err != nil {
			return "", 0, nil, err
		}
	}

	return status, responseCode, []byte(responseBody), err
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
