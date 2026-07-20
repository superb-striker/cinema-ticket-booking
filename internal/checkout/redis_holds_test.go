package checkout

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func testHoldStore(t *testing.T) (*RedisHoldStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewRedisHoldStore(client), mr
}

func TestRedisHoldStoreMultiSeatIsAtomic(t *testing.T) {
	store, _ := testHoldStore(t)
	ctx := context.Background()
	screeningID := uuid.New()
	first, err := store.Create(ctx, Hold{ScreeningID: screeningID, UserID: uuid.New(), SeatIDs: []string{"A1", "A2"}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Create(ctx, Hold{ScreeningID: screeningID, UserID: uuid.New(), SeatIDs: []string{"A2", "A3"}}, time.Minute)
	if !errors.Is(err, ErrSeatsUnavailable) {
		t.Fatalf("expected unavailable, got %v", err)
	}
	held, err := store.HeldSeats(ctx, screeningID)
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 2 {
		t.Fatalf("second hold partially wrote inventory: %v", held)
	}
	got, err := store.Get(ctx, screeningID, first.ID)
	if err != nil || got.ID != first.ID {
		t.Fatalf("get hold: %+v %v", got, err)
	}
}

func TestRedisHoldStoreExpiresAllSeats(t *testing.T) {
	store, mr := testHoldStore(t)
	ctx := context.Background()
	screeningID := uuid.New()
	hold, err := store.Create(ctx, Hold{ScreeningID: screeningID, UserID: uuid.New(), SeatIDs: []string{"A1", "A2"}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	mr.FastForward(time.Minute + time.Second)
	if _, err := store.Get(ctx, screeningID, hold.ID); !errors.Is(err, ErrHoldNotFound) {
		t.Fatalf("expected expired hold, got %v", err)
	}
	if _, err := store.Create(ctx, Hold{ScreeningID: screeningID, UserID: uuid.New(), SeatIDs: []string{"A1", "A2"}}, time.Minute); err != nil {
		t.Fatalf("seats were not released: %v", err)
	}
}
