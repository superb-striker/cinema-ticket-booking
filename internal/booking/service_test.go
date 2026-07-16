package booking

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// newRedisStore starts an isolated in-process Redis (with Lua support).
// No external Redis needed, and nothing persists between runs.
func newRedisStore(t *testing.T) (*RedisStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewRedisStore(rdb), mr
}

func forEachStore(t *testing.T, fn func(t *testing.T, store BookingStore)) {
	t.Run("redis", func(t *testing.T) {
		store, _ := newRedisStore(t)
		fn(t, store)
	})
	t.Run("memory", func(t *testing.T) {
		fn(t, NewMemoryStore())
	})
}

func TestConcurrentBooking_ExactlyOneWins(t *testing.T) {
	forEachStore(t, func(t *testing.T, store BookingStore) {
		svc := NewService(store)
		const n = 1000

		var successes, conflicts, other atomic.Int64
		var wg sync.WaitGroup

		wg.Add(n)
		for range n {
			go func() {
				defer wg.Done()
				_, err := svc.Book(context.Background(), Booking{
					MovieID: "screen-1",
					SeatID:  "A1",
					UserID:  uuid.New().String(),
				})
				switch {
				case err == nil:
					successes.Add(1)
				case errors.Is(err, ErrSeatAlreadyBooked):
					conflicts.Add(1)
				default:
					other.Add(1)
				}
			}()
		}
		wg.Wait()

		if got := successes.Load(); got != 1 {
			t.Errorf("expected exactly 1 success, got %d", got)
		}
		if got := conflicts.Load(); got != n-1 {
			t.Errorf("expected %d conflicts, got %d", n-1, got)
		}
		if got := other.Load(); got != 0 {
			t.Errorf("expected 0 unexpected errors, got %d", got)
		}
	})
}

func TestConfirm_RequiresOwner(t *testing.T) {
	forEachStore(t, func(t *testing.T, store BookingStore) {
		ctx := context.Background()
		held, err := store.Book(ctx, Booking{MovieID: "m", SeatID: "A1", UserID: "alice"})
		if err != nil {
			t.Fatal(err)
		}

		if _, err := store.Confirm(ctx, held.ID, "mallory"); !errors.Is(err, ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got %v", err)
		}
		if err := store.Release(ctx, held.ID, "mallory"); !errors.Is(err, ErrForbidden) {
			t.Fatalf("expected ErrForbidden on release, got %v", err)
		}
		if _, err := store.Confirm(ctx, "no-such-session", "alice"); !errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("expected ErrSessionNotFound, got %v", err)
		}

		got, err := store.Confirm(ctx, held.ID, "alice")
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != StatusConfirmed {
			t.Errorf("expected confirmed, got %q", got.Status)
		}

		// Confirmed bookings can't be released through the hold session.
		if err := store.Release(ctx, held.ID, "alice"); !errors.Is(err, ErrNotHeld) {
			t.Fatalf("expected ErrNotHeld, got %v", err)
		}
	})
}

func TestRelease_FreesSeat(t *testing.T) {
	forEachStore(t, func(t *testing.T, store BookingStore) {
		ctx := context.Background()
		held, err := store.Book(ctx, Booking{MovieID: "m", SeatID: "A1", UserID: "alice"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Release(ctx, held.ID, "alice"); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Book(ctx, Booking{MovieID: "m", SeatID: "A1", UserID: "bob"}); err != nil {
			t.Fatalf("seat should be free after release: %v", err)
		}
	})
}

func TestListBookings_ScopedToMovieAndIncludesExpiry(t *testing.T) {
	forEachStore(t, func(t *testing.T, store BookingStore) {
		ctx := context.Background()
		_, _ = store.Book(ctx, Booking{MovieID: "m1", SeatID: "A1", UserID: "alice"})
		_, _ = store.Book(ctx, Booking{MovieID: "m2", SeatID: "A1", UserID: "bob"}) // same seat, other movie

		list, err := store.ListBookings(ctx, "m1")
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 1 || list[0].UserID != "alice" {
			t.Fatalf("unexpected list: %+v", list)
		}
		if list[0].ExpiresAt.IsZero() {
			t.Error("held booking should carry ExpiresAt")
		}
	})
}

func TestRedis_HoldExpiryFreesSeat(t *testing.T) {
	store, mr := newRedisStore(t)
	ctx := context.Background()

	held, err := store.Book(ctx, Booking{MovieID: "m", SeatID: "A1", UserID: "alice"})
	if err != nil {
		t.Fatal(err)
	}

	mr.FastForward(defaultHoldTTL + time.Second)

	// Expired hold can no longer be confirmed (no resurrection)...
	if _, err := store.Confirm(ctx, held.ID, "alice"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got %v", err)
	}
	// ...and the seat is bookable again.
	if _, err := store.Book(ctx, Booking{MovieID: "m", SeatID: "A1", UserID: "bob"}); err != nil {
		t.Fatalf("seat should be free after expiry: %v", err)
	}
	// Listing shows only the new hold and prunes the stale index entry.
	list, err := store.ListBookings(ctx, "m")
	if err != nil || len(list) != 1 || list[0].UserID != "bob" {
		t.Fatalf("unexpected list %+v, err %v", list, err)
	}
}

func TestMemory_HoldExpiryFreesSeat(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now()
	store.now = func() time.Time { return now }
	ctx := context.Background()

	held, _ := store.Book(ctx, Booking{MovieID: "m", SeatID: "A1", UserID: "alice"})
	now = now.Add(defaultHoldTTL + time.Second)

	if _, err := store.Confirm(ctx, held.ID, "alice"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got %v", err)
	}
	if _, err := store.Book(ctx, Booking{MovieID: "m", SeatID: "A1", UserID: "bob"}); err != nil {
		t.Fatalf("seat should be free after expiry: %v", err)
	}
}
