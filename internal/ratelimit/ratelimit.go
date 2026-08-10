package ratelimit

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrUnavailable = errors.New("rate limit service unavailable")

type Result struct {
	Allowed    bool
	Requests   int64
	Tokens     int64
	RetryAfter time.Duration
}
type Limiter struct{ client *redis.Client }

func New(client *redis.Client) *Limiter { return &Limiter{client: client} }

var script = redis.NewScript(`
local rpm=redis.call('INCR',KEYS[1])
if rpm==1 then redis.call('EXPIRE',KEYS[1],ARGV[3]) end
local tpm=redis.call('INCRBY',KEYS[2],ARGV[1])
if tpm==tonumber(ARGV[1]) then redis.call('EXPIRE',KEYS[2],ARGV[3]) end
if rpm>tonumber(ARGV[2]) or tpm>tonumber(ARGV[4]) then return {0,rpm,tpm,redis.call('TTL',KEYS[1])} end
return {1,rpm,tpm,redis.call('TTL',KEYS[1])}`)

func (l *Limiter) Allow(ctx context.Context, keyID string, rpm, tpm, estimatedTokens int) (Result, error) {
	if rpm <= 0 {
		rpm = 60
	}
	if tpm <= 0 {
		tpm = 100000
	}
	if estimatedTokens < 1 {
		estimatedTokens = 1
	}
	bucket := time.Now().UTC().Format("200601021504")
	v, err := script.Run(ctx, l.client, []string{"rdk:rl:r:" + keyID + ":" + bucket, "rdk:rl:t:" + keyID + ":" + bucket}, estimatedTokens, rpm, 70, tpm).Slice()
	if err != nil {
		return Result{}, ErrUnavailable
	}
	allowed, _ := v[0].(int64)
	requests, _ := v[1].(int64)
	tokens, _ := v[2].(int64)
	ttl, _ := v[3].(int64)
	if ttl < 1 {
		ttl = 60
	}
	return Result{Allowed: allowed == 1, Requests: requests, Tokens: tokens, RetryAfter: time.Duration(ttl) * time.Second}, nil
}
