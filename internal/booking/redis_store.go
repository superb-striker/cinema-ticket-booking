package booking

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const defaultHoldTTL = 2 * time.Minute

// RedisStore implements session-based seat booking backed by Redis.
//
// Key design:
//
//	seat:{movieID}:{seatID}  → booking JSON (TTL = held, no TTL = confirmed)
//	session:{sessionID}      → seat key      (reverse lookup, same TTL)
//	movie:{movieID}:seats    → set of seat keys (index, so listing needs no SCAN)
//
// Every multi-key mutation runs as a Lua script, so each is atomic.
type RedisStore struct {
	rdb *redis.Client
	ttl time.Duration
}

func NewRedisStore(rdb *redis.Client) *RedisStore {
	return &RedisStore{rdb: rdb, ttl: defaultHoldTTL}
}

func seatKey(movieID, seatID string) string { return fmt.Sprintf("seat:%s:%s", movieID, seatID) }
func sessionKey(id string) string           { return fmt.Sprintf("session:%s", id) }
func indexKey(movieID string) string        { return fmt.Sprintf("movie:%s:seats", movieID) }

// KEYS: seatKey, sessionKey, indexKey   ARGV: bookingJSON, ttlMillis
// Returns 1 on success, 0 if the seat is already taken.
var holdScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 then
  return 0
end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
redis.call('SET', KEYS[2], KEYS[1], 'PX', ARGV[2])
redis.call('SADD', KEYS[3], KEYS[1])
return 1
`)

// KEYS: sessionKey   ARGV: userID, sessionID
// Returns the confirmed booking JSON, or 0 (not found), -1 (wrong user).
var confirmScript = redis.NewScript(`
local seatKey = redis.call('GET', KEYS[1])
if not seatKey then return 0 end
local raw = redis.call('GET', seatKey)
if not raw then return 0 end
local b = cjson.decode(raw)
if b.id ~= ARGV[2] then return 0 end
if b.user_id ~= ARGV[1] then return -1 end
if b.status == 'confirmed' then return raw end
b.status = 'confirmed'
b.expires_at = '0001-01-01T00:00:00Z'
local out = cjson.encode(b)
redis.call('SET', seatKey, out)
redis.call('PERSIST', KEYS[1])
return out
`)

// KEYS: sessionKey   ARGV: userID, sessionID
// Returns 1 on success, 0 (not found), -1 (wrong user), -2 (already confirmed).
var releaseScript = redis.NewScript(`
local seatKey = redis.call('GET', KEYS[1])
if not seatKey then return 0 end
local raw = redis.call('GET', seatKey)
if not raw then return 0 end
local b = cjson.decode(raw)
if b.id ~= ARGV[2] then return 0 end
if b.user_id ~= ARGV[1] then return -1 end
if b.status ~= 'held' then return -2 end
redis.call('DEL', seatKey, KEYS[1])
return 1
`)

// KEYS: indexKey   ARGV: seat keys to check
// Removes index entries whose seat key no longer exists. Done in Lua so a
// concurrent hold on the same seat can't be pruned by mistake.
var pruneScript = redis.NewScript(`
for i = 1, #ARGV do
  if redis.call('EXISTS', ARGV[i]) == 0 then
    redis.call('SREM', KEYS[1], ARGV[i])
  end
end
return 1
`)

func scriptErr(code int64) error {
	switch code {
	case 0:
		return ErrSessionNotFound
	case -1:
		return ErrForbidden
	case -2:
		return ErrNotHeld
	default:
		return fmt.Errorf("unexpected script code %d", code)
	}
}

func (s *RedisStore) Book(ctx context.Context, b Booking) (Booking, error) {
	b.ID = uuid.New().String()
	b.Status = StatusHeld
	b.ExpiresAt = time.Now().Add(s.ttl)

	val, err := json.Marshal(b)
	if err != nil {
		return Booking{}, fmt.Errorf("marshal booking: %w", err)
	}

	keys := []string{seatKey(b.MovieID, b.SeatID), sessionKey(b.ID), indexKey(b.MovieID)}
	code, err := holdScript.Run(ctx, s.rdb, keys, string(val), s.ttl.Milliseconds()).Int()
	if err != nil {
		// A real Redis/network failure, not "seat taken".
		return Booking{}, fmt.Errorf("hold seat: %w", err)
	}
	if code == 0 {
		return Booking{}, ErrSeatAlreadyBooked
	}
	return b, nil
}

func (s *RedisStore) ListBookings(ctx context.Context, movieID string) ([]Booking, error) {
	idx := indexKey(movieID)

	keys, err := s.rdb.SMembers(ctx, idx).Result()
	if err != nil {
		return nil, fmt.Errorf("list index: %w", err)
	}
	if len(keys) == 0 {
		return nil, nil
	}

	vals, err := s.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("mget seats: %w", err)
	}

	result := make([]Booking, 0, len(keys))
	var stale []interface{}
	for i, v := range vals {
		str, ok := v.(string)
		if !ok { // nil → hold expired or released
			stale = append(stale, keys[i])
			continue
		}
		b, err := parseBooking(str)
		if err != nil {
			continue
		}
		result = append(result, b)
	}

	if len(stale) > 0 {
		// Best-effort cleanup; failure here doesn't affect the response.
		_ = pruneScript.Run(ctx, s.rdb, []string{idx}, stale...).Err()
	}
	return result, nil
}

func parseBooking(val string) (Booking, error) {
	var b Booking
	if err := json.Unmarshal([]byte(val), &b); err != nil {
		return Booking{}, err
	}
	return b, nil
}

// Confirm atomically converts a held session into a permanent booking.
func (s *RedisStore) Confirm(ctx context.Context, sessionID, userID string) (Booking, error) {
	res, err := confirmScript.Run(ctx, s.rdb, []string{sessionKey(sessionID)}, userID, sessionID).Result()
	if err != nil {
		return Booking{}, fmt.Errorf("confirm: %w", err)
	}
	switch v := res.(type) {
	case int64:
		return Booking{}, scriptErr(v)
	case string:
		return parseBooking(v)
	default:
		return Booking{}, fmt.Errorf("confirm: unexpected reply %T", res)
	}
}

// Release atomically frees a held seat. Confirmed bookings can't be released here.
func (s *RedisStore) Release(ctx context.Context, sessionID, userID string) error {
	code, err := releaseScript.Run(ctx, s.rdb, []string{sessionKey(sessionID)}, userID, sessionID).Int64()
	if err != nil {
		return fmt.Errorf("release: %w", err)
	}
	if code != 1 {
		return scriptErr(code)
	}
	return nil
}
