package ratelimit

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
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

var requestOnlyScript = redis.NewScript(`
local n=redis.call('INCR',KEYS[1])
if n==1 then redis.call('EXPIRE',KEYS[1],70) end
local ttl=redis.call('TTL',KEYS[1])
if n>tonumber(ARGV[1]) then return {0,n,ttl} end
return {1,n,ttl}`)

// AllowOrganization enforces the subscription request entitlement across all
// API keys owned by an organization. It is independent from each key's
// backwards-compatible RPM/TPM bucket.
func (l *Limiter) AllowOrganization(ctx context.Context, organizationID string, rpm int64) (Result, error) {
	if l == nil || l.client == nil || rpm <= 0 {
		return Result{}, ErrUnavailable
	}
	bucket := time.Now().UTC().Format("200601021504")
	values, err := requestOnlyScript.Run(ctx, l.client, []string{"rdk:subscription:r:" + organizationID + ":" + bucket}, rpm).Slice()
	if err != nil {
		return Result{}, ErrUnavailable
	}
	allowed, _ := values[0].(int64)
	requests, _ := values[1].(int64)
	ttl, _ := values[2].(int64)
	if ttl < 1 {
		ttl = 60
	}
	return Result{Allowed: allowed == 1, Requests: requests, RetryAfter: time.Duration(ttl) * time.Second}, nil
}

// AllowProvider enforces the commercially configured aggregate Provider RPM
// independently from technical credential concurrency and customer limits.
func (l *Limiter) AllowProvider(ctx context.Context, providerID string, rpm int64) (Result, error) {
	if l == nil || l.client == nil || rpm <= 0 {
		return Result{Allowed: true}, nil
	}
	bucket := time.Now().UTC().Format("200601021504")
	values, err := requestOnlyScript.Run(ctx, l.client, []string{"rdk:provider:r:" + providerID + ":" + bucket}, rpm).Slice()
	if err != nil {
		return Result{}, ErrUnavailable
	}
	allowed, _ := values[0].(int64)
	requests, _ := values[1].(int64)
	ttl, _ := values[2].(int64)
	if ttl < 1 {
		ttl = 60
	}
	return Result{Allowed: allowed == 1, Requests: requests, RetryAfter: time.Duration(ttl) * time.Second}, nil
}

var concurrencyAcquireScript = redis.NewScript(`
local n=redis.call('INCR',KEYS[1])
if n==1 then redis.call('EXPIRE',KEYS[1],7200) end
if n>tonumber(ARGV[1]) then redis.call('DECR',KEYS[1]); return {0,n-1} end
return {1,n}`)
var concurrencyReleaseScript = redis.NewScript(`
local n=tonumber(redis.call('GET',KEYS[1]) or '0')
if n<=1 then redis.call('DEL',KEYS[1]); return 0 end
return redis.call('DECR',KEYS[1])`)

// AcquireOrganizationConcurrency returns a release function only after an
// atomic organization-wide slot is acquired. Redis failure fails closed.
func (l *Limiter) AcquireOrganizationConcurrency(ctx context.Context, organizationID string, limit int64) (func(context.Context) error, int64, error) {
	if l == nil || l.client == nil || limit <= 0 {
		return nil, 0, ErrUnavailable
	}
	key := "rdk:subscription:active:" + organizationID
	values, err := concurrencyAcquireScript.Run(ctx, l.client, []string{key}, limit).Slice()
	if err != nil {
		return nil, 0, ErrUnavailable
	}
	allowed, _ := values[0].(int64)
	active, _ := values[1].(int64)
	if allowed != 1 {
		return nil, active, nil
	}
	released := false
	return func(releaseContext context.Context) error {
		if released {
			return nil
		}
		released = true
		return concurrencyReleaseScript.Run(releaseContext, l.client, []string{key}).Err()
	}, active, nil
}

var identityScript = redis.NewScript(`
local n=redis.call('INCR',KEYS[1])
if n==1 then redis.call('PEXPIRE',KEYS[1],ARGV[2]) end
local ttl=redis.call('PTTL',KEYS[1])
if n>tonumber(ARGV[1]) then return {0,n,ttl} end
return {1,n,ttl}`)

// AllowIdentity keeps authentication controls in independent buckets. The
// supplied identity is hashed before it becomes part of a Redis key so email
// addresses and client addresses are not exposed in Redis diagnostics.
func (l *Limiter) AllowIdentity(ctx context.Context, action, identity string, limit int, window time.Duration) (Result, error) {
	if l == nil || l.client == nil {
		return Result{}, ErrUnavailable
	}
	if limit <= 0 {
		limit = 5
	}
	if window <= 0 {
		window = 15 * time.Minute
	}
	digest := sha256.Sum256([]byte(identity))
	key := fmt.Sprintf("rdk:identity:%s:%x", action, digest[:16])
	values, err := identityScript.Run(ctx, l.client, []string{key}, limit, window.Milliseconds()).Slice()
	if err != nil {
		return Result{}, ErrUnavailable
	}
	allowed, _ := values[0].(int64)
	requests, _ := values[1].(int64)
	ttlMS, _ := values[2].(int64)
	if ttlMS < 1 {
		ttlMS = window.Milliseconds()
	}
	return Result{Allowed: allowed == 1, Requests: requests, RetryAfter: time.Duration(ttlMS) * time.Millisecond}, nil
}
