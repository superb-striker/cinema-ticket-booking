package checkout

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type RedisHoldStore struct{ rdb *redis.Client }

func NewRedisHoldStore(rdb *redis.Client) *RedisHoldStore { return &RedisHoldStore{rdb: rdb} }

func holdKey(screeningID, holdID uuid.UUID) string {
	return fmt.Sprintf("cinema:{%s}:hold:%s", screeningID, holdID)
}
func heldIndexKey(screeningID uuid.UUID) string {
	return fmt.Sprintf("cinema:{%s}:held-seats", screeningID)
}
func heldSeatKey(screeningID uuid.UUID, seatID string) string {
	return fmt.Sprintf("cinema:{%s}:seat:%s", screeningID, seatID)
}

var createHoldScript = redis.NewScript(`
for i = 3, #KEYS do
  if redis.call('EXISTS', KEYS[i]) == 1 then return 0 end
end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
for i = 3, #KEYS do
  redis.call('SET', KEYS[i], ARGV[3], 'PX', ARGV[2])
  redis.call('ZADD', KEYS[2], ARGV[4], ARGV[i + 2])
end
redis.call('PEXPIRE', KEYS[2], ARGV[2] + 60000)
return 1
`)

var releaseHoldScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
redis.call('DEL', KEYS[1])
for i = 3, #KEYS do
  if redis.call('GET', KEYS[i]) == ARGV[2] then redis.call('DEL', KEYS[i]) end
  redis.call('ZREM', KEYS[2], ARGV[i])
end
return 1
`)

func (s *RedisHoldStore) Create(ctx context.Context, hold Hold, ttl time.Duration) (Hold, error) {
	hold.ID = uuid.New()
	hold.ExpiresAt = time.Now().UTC().Add(ttl)
	payload, err := json.Marshal(hold)
	if err != nil {
		return Hold{}, err
	}
	keys := []string{holdKey(hold.ScreeningID, hold.ID), heldIndexKey(hold.ScreeningID)}
	args := []any{string(payload), ttl.Milliseconds(), hold.ID.String(), hold.ExpiresAt.UnixMilli()}
	for _, seatID := range hold.SeatIDs {
		keys = append(keys, heldSeatKey(hold.ScreeningID, seatID))
		args = append(args, seatID)
	}
	result, err := createHoldScript.Run(ctx, s.rdb, keys, args...).Int()
	if err != nil {
		return Hold{}, fmt.Errorf("create hold: %w", err)
	}
	if result == 0 {
		return Hold{}, ErrSeatsUnavailable
	}
	return hold, nil
}

func (s *RedisHoldStore) Get(ctx context.Context, screeningID, id uuid.UUID) (Hold, error) {
	raw, err := s.rdb.Get(ctx, holdKey(screeningID, id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return Hold{}, ErrHoldNotFound
	}
	if err != nil {
		return Hold{}, err
	}
	var hold Hold
	if err := json.Unmarshal(raw, &hold); err != nil {
		return Hold{}, err
	}
	return hold, nil
}

func (s *RedisHoldStore) Release(ctx context.Context, hold Hold) error {
	keys := []string{holdKey(hold.ScreeningID, hold.ID), heldIndexKey(hold.ScreeningID)}
	args := []any{mustJSON(hold), hold.ID.String()}
	for _, seatID := range hold.SeatIDs {
		keys = append(keys, heldSeatKey(hold.ScreeningID, seatID))
		args = append(args, seatID)
	}
	_, err := releaseHoldScript.Run(ctx, s.rdb, keys, args...).Result()
	return err
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func (s *RedisHoldStore) HeldSeats(ctx context.Context, screeningID uuid.UUID) ([]string, error) {
	key := heldIndexKey(screeningID)
	pipe := s.rdb.TxPipeline()
	pipe.ZRemRangeByScore(ctx, key, "-inf", fmt.Sprint(time.Now().UTC().UnixMilli()))
	list := pipe.ZRange(ctx, key, 0, -1)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	return list.Val(), nil
}
