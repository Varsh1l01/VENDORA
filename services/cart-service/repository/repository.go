// Package repository stores carts as Redis hashes: cart:{user} maps
// product_id -> quantity. All mutations are single Lua scripts, so they are
// atomic and free of read-modify-write races.
package repository

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Varsh1l01/VENDORA/pkg/apperr"
)

// TTL is the sliding expiry of an idle cart.
const TTL = 30 * 24 * time.Hour

// Line is a cart line as stored (quantities only; prices live in the catalog).
type Line struct {
	ProductID string
	Quantity  int32
}

// Redis implements the cart store.
type Redis struct{ c *redis.Client }

// New returns a Redis store.
func New(c *redis.Client) *Redis { return &Redis{c: c} }

func key(userID string) string { return "cart:v1:" + userID }

// addScript increments a line, capping it at ARGV[3] and refusing new lines
// beyond ARGV[4]. Returns the new quantity, or -1 when the cart is full.
var addScript = redis.NewScript(`
local exists = redis.call('HEXISTS', KEYS[1], ARGV[1])
if exists == 0 and redis.call('HLEN', KEYS[1]) >= tonumber(ARGV[4]) then
  return -1
end
local q = redis.call('HINCRBY', KEYS[1], ARGV[1], ARGV[2])
if q > tonumber(ARGV[3]) then
  redis.call('HSET', KEYS[1], ARGV[1], ARGV[3])
  q = tonumber(ARGV[3])
end
redis.call('PEXPIRE', KEYS[1], ARGV[5])
return q
`)

// setScript sets a line's quantity (0 removes it), with the same line limit.
var setScript = redis.NewScript(`
if tonumber(ARGV[2]) <= 0 then
  redis.call('HDEL', KEYS[1], ARGV[1])
  return 0
end
local exists = redis.call('HEXISTS', KEYS[1], ARGV[1])
if exists == 0 and redis.call('HLEN', KEYS[1]) >= tonumber(ARGV[3]) then
  return -1
end
redis.call('HSET', KEYS[1], ARGV[1], ARGV[2])
redis.call('PEXPIRE', KEYS[1], ARGV[4])
return tonumber(ARGV[2])
`)

// subtractScript removes ordered quantities exactly once per event id.
// KEYS[1]=cart, KEYS[2]=dedupe marker; ARGV = ttl_ms, pid1, qty1, pid2, qty2...
var subtractScript = redis.NewScript(`
if redis.call('SET', KEYS[2], '1', 'NX', 'PX', ARGV[1]) == false then
  return 0
end
for i = 2, #ARGV, 2 do
  local q = redis.call('HINCRBY', KEYS[1], ARGV[i], -tonumber(ARGV[i+1]))
  if q <= 0 then redis.call('HDEL', KEYS[1], ARGV[i]) end
end
return 1
`)

// Add increments productID by qty, capped at maxQty. Returns ErrCartFull.
func (r *Redis) Add(ctx context.Context, userID, productID string, qty, maxQty int32, maxLines int) error {
	n, err := addScript.Run(ctx, r.c, []string{key(userID)}, productID, qty, maxQty, maxLines, TTL.Milliseconds()).Int()
	if err != nil {
		return apperr.Unavailable(err, "cart add")
	}
	if n < 0 {
		return apperr.FailedPrecondition("cart cannot hold more than %d different products", maxLines)
	}
	return nil
}

// Set sets productID's quantity; 0 removes the line.
func (r *Redis) Set(ctx context.Context, userID, productID string, qty int32, maxLines int) error {
	n, err := setScript.Run(ctx, r.c, []string{key(userID)}, productID, qty, maxLines, TTL.Milliseconds()).Int()
	if err != nil {
		return apperr.Unavailable(err, "cart set")
	}
	if n < 0 {
		return apperr.FailedPrecondition("cart cannot hold more than %d different products", maxLines)
	}
	return nil
}

// Remove deletes a line.
func (r *Redis) Remove(ctx context.Context, userID, productID string) error {
	if err := r.c.HDel(ctx, key(userID), productID).Err(); err != nil {
		return apperr.Unavailable(err, "cart remove")
	}
	return nil
}

// Clear deletes the cart.
func (r *Redis) Clear(ctx context.Context, userID string) error {
	if err := r.c.Del(ctx, key(userID)).Err(); err != nil {
		return apperr.Unavailable(err, "cart clear")
	}
	return nil
}

// Lines returns all lines of a cart.
func (r *Redis) Lines(ctx context.Context, userID string) ([]Line, error) {
	m, err := r.c.HGetAll(ctx, key(userID)).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, apperr.Unavailable(err, "cart get")
	}
	out := make([]Line, 0, len(m))
	for pid, q := range m {
		n, err := strconv.Atoi(q)
		if err != nil || n <= 0 {
			continue
		}
		out = append(out, Line{ProductID: pid, Quantity: int32(n)})
	}
	return out, nil
}

// SubtractOrdered removes the ordered quantities from the cart once per
// eventID (duplicate deliveries are no-ops). Items added after checkout stay.
func (r *Redis) SubtractOrdered(ctx context.Context, eventID, userID string, lines []Line) error {
	args := []any{(7 * 24 * time.Hour).Milliseconds()}
	for _, l := range lines {
		args = append(args, l.ProductID, l.Quantity)
	}
	err := subtractScript.Run(ctx, r.c, []string{key(userID), "cart:processed:" + eventID}, args...).Err()
	if err != nil {
		return apperr.Unavailable(err, "cart subtract")
	}
	return nil
}

// Ping checks Redis connectivity.
func (r *Redis) Ping(ctx context.Context) error { return r.c.Ping(ctx).Err() }
