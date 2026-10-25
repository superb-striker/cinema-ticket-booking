package booking

import (
	"context"
	"errors"
	"time"
)

var (
	ErrSeatAlreadyBooked = errors.New("seat is already taken")
	ErrSessionNotFound   = errors.New("session not found or expired")
	ErrForbidden         = errors.New("session belongs to another user")
	ErrNotHeld           = errors.New("session is not in held state")
)

const (
	StatusHeld      = "held"
	StatusConfirmed = "confirmed"
)

// Booking represents a seat hold or a confirmed reservation.
// ExpiresAt is zero once the booking is confirmed.
type Booking struct {
	ID        string    `json:"id"`
	MovieID   string    `json:"movie_id"`
	SeatID    string    `json:"seat_id"`
	UserID    string    `json:"user_id"`
	Status    string    `json:"status"`
	ExpiresAt time.Time `json:"expires_at"`
}

type BookingStore interface {
	Book(ctx context.Context, b Booking) (Booking, error)
	ListBookings(ctx context.Context, movieID string) ([]Booking, error)

	Confirm(ctx context.Context, sessionID string, userID string) (Booking, error)
	Release(ctx context.Context, sessionID string, userID string) error
}
