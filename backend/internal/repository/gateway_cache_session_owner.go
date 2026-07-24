package repository

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

var claimSessionAccountScript = redis.NewScript(`
local owner = redis.call('GET', KEYS[1])
if owner then
  redis.call('DEL', KEYS[2])
  if owner == ARGV[1] then
    redis.call('PEXPIRE', KEYS[1], ARGV[2])
  end
  return {owner, 0}
end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
redis.call('DEL', KEYS[2])
return {ARGV[1], 1}
`)

var claimSessionAccountWithRollbackTokenScript = redis.NewScript(`
local owner = redis.call('GET', KEYS[1])
if owner then
  if owner == ARGV[1] then
    redis.call('PEXPIRE', KEYS[1], ARGV[2])
  end
  return {owner, 0}
end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
redis.call('SET', KEYS[2], ARGV[3], 'PX', ARGV[2])
return {ARGV[1], 1}
`)

var compareAndSwapSessionAccountScript = redis.NewScript(`
local owner = redis.call('GET', KEYS[1])
if owner then
  redis.call('DEL', KEYS[2])
end
if owner and owner == ARGV[1] then
  redis.call('SET', KEYS[1], ARGV[2], 'PX', ARGV[3])
  return 1
end
return 0
`)

var compareAndSwapSessionAccountWithRollbackTokenScript = redis.NewScript(`
local owner = redis.call('GET', KEYS[1])
if owner and owner == ARGV[1] then
  redis.call('SET', KEYS[1], ARGV[2], 'PX', ARGV[3])
  redis.call('SET', KEYS[2], ARGV[4], 'PX', ARGV[3])
  return 1
end
return 0
`)

var refreshSessionTTLIfOwnerScript = redis.NewScript(`
local owner = redis.call('GET', KEYS[1])
if owner and owner == ARGV[1] then
  redis.call('PEXPIRE', KEYS[1], ARGV[2])
  redis.call('DEL', KEYS[2])
  return 1
end
return 0
`)

var deleteSessionAccountIfOwnerScript = redis.NewScript(`
local owner = redis.call('GET', KEYS[1])
if owner then
  redis.call('DEL', KEYS[2])
end
if owner and owner == ARGV[1] then
  redis.call('DEL', KEYS[1])
  return 1
end
return 0
`)

var rollbackSessionAccountScript = redis.NewScript(`
local owner = redis.call('GET', KEYS[1])
local token = redis.call('GET', KEYS[2])
if not owner or owner ~= ARGV[1] or not token or token ~= ARGV[4] then
  return 0
end
if ARGV[2] == '0' then
  redis.call('DEL', KEYS[1])
else
  redis.call('SET', KEYS[1], ARGV[2], 'PX', ARGV[3])
end
redis.call('DEL', KEYS[2])
return 1
`)

func sessionRollbackGuardKey(sessionKey string) string {
	return sessionKey + ":rollback_guard"
}

func sessionOwnerTTLMillis(ttl time.Duration) int64 {
	millis := ttl.Milliseconds()
	if millis < 1 {
		return 1
	}
	return millis
}

func (c *gatewayCache) ClaimSessionAccount(ctx context.Context, groupID int64, sessionHash string, accountID int64, ttl time.Duration) (int64, bool, error) {
	sessionKey := buildSessionKey(groupID, sessionHash)
	result, err := claimSessionAccountScript.Run(
		ctx,
		c.rdb,
		[]string{sessionKey, sessionRollbackGuardKey(sessionKey)},
		accountID,
		sessionOwnerTTLMillis(ttl),
	).Result()
	if err != nil {
		return 0, false, err
	}
	values, ok := result.([]any)
	if !ok || len(values) != 2 {
		return 0, false, fmt.Errorf("unexpected claim session result %T", result)
	}
	ownerID, err := strconv.ParseInt(fmt.Sprint(values[0]), 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("parse claimed session owner: %w", err)
	}
	claimed, err := strconv.ParseInt(fmt.Sprint(values[1]), 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("parse claimed session status: %w", err)
	}
	return ownerID, claimed == 1, nil
}

func (c *gatewayCache) ClaimSessionAccountWithRollbackToken(
	ctx context.Context,
	groupID int64,
	sessionHash string,
	accountID int64,
	ttl time.Duration,
	rollbackToken string,
) (int64, bool, error) {
	sessionKey := buildSessionKey(groupID, sessionHash)
	result, err := claimSessionAccountWithRollbackTokenScript.Run(
		ctx,
		c.rdb,
		[]string{sessionKey, sessionRollbackGuardKey(sessionKey)},
		accountID,
		sessionOwnerTTLMillis(ttl),
		rollbackToken,
	).Result()
	if err != nil {
		return 0, false, err
	}
	values, ok := result.([]any)
	if !ok || len(values) != 2 {
		return 0, false, fmt.Errorf("unexpected guarded claim session result %T", result)
	}
	ownerID, err := strconv.ParseInt(fmt.Sprint(values[0]), 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("parse guarded claimed session owner: %w", err)
	}
	claimed, err := strconv.ParseInt(fmt.Sprint(values[1]), 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("parse guarded claimed session status: %w", err)
	}
	return ownerID, claimed == 1, nil
}

func (c *gatewayCache) CompareAndSwapSessionAccount(ctx context.Context, groupID int64, sessionHash string, oldAccountID, newAccountID int64, ttl time.Duration) (bool, error) {
	sessionKey := buildSessionKey(groupID, sessionHash)
	result, err := compareAndSwapSessionAccountScript.Run(
		ctx,
		c.rdb,
		[]string{sessionKey, sessionRollbackGuardKey(sessionKey)},
		oldAccountID,
		newAccountID,
		sessionOwnerTTLMillis(ttl),
	).Int64()
	return result == 1, err
}

func (c *gatewayCache) CompareAndSwapSessionAccountWithRollbackToken(
	ctx context.Context,
	groupID int64,
	sessionHash string,
	oldAccountID int64,
	newAccountID int64,
	ttl time.Duration,
	rollbackToken string,
) (bool, error) {
	sessionKey := buildSessionKey(groupID, sessionHash)
	result, err := compareAndSwapSessionAccountWithRollbackTokenScript.Run(
		ctx,
		c.rdb,
		[]string{sessionKey, sessionRollbackGuardKey(sessionKey)},
		oldAccountID,
		newAccountID,
		sessionOwnerTTLMillis(ttl),
		rollbackToken,
	).Int64()
	return result == 1, err
}

func (c *gatewayCache) RefreshSessionTTLIfOwner(ctx context.Context, groupID int64, sessionHash string, accountID int64, ttl time.Duration) (bool, error) {
	sessionKey := buildSessionKey(groupID, sessionHash)
	result, err := refreshSessionTTLIfOwnerScript.Run(
		ctx,
		c.rdb,
		[]string{sessionKey, sessionRollbackGuardKey(sessionKey)},
		accountID,
		sessionOwnerTTLMillis(ttl),
	).Int64()
	return result == 1, err
}

func (c *gatewayCache) DeleteSessionAccountIfOwner(ctx context.Context, groupID int64, sessionHash string, accountID int64) (bool, error) {
	sessionKey := buildSessionKey(groupID, sessionHash)
	result, err := deleteSessionAccountIfOwnerScript.Run(
		ctx,
		c.rdb,
		[]string{sessionKey, sessionRollbackGuardKey(sessionKey)},
		accountID,
	).Int64()
	return result == 1, err
}

func (c *gatewayCache) RollbackSessionAccount(
	ctx context.Context,
	groupID int64,
	sessionHash string,
	currentAccountID int64,
	previousAccountID int64,
	ttl time.Duration,
	rollbackToken string,
) (bool, error) {
	sessionKey := buildSessionKey(groupID, sessionHash)
	result, err := rollbackSessionAccountScript.Run(
		ctx,
		c.rdb,
		[]string{sessionKey, sessionRollbackGuardKey(sessionKey)},
		currentAccountID,
		previousAccountID,
		sessionOwnerTTLMillis(ttl),
		rollbackToken,
	).Int64()
	return result == 1, err
}

var _ service.OpenAISessionOwnerCache = (*gatewayCache)(nil)
var _ service.OpenAISessionOwnerRollbackCache = (*gatewayCache)(nil)
