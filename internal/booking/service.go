package booking

import "context"

type Service struct {
	store BookingStore
}

func NewService(store BookingStore) *Service {
	return &Service{store}
}

func (s *Service) Book(ctx context.Context, b Booking) (Booking, error) {
	return s.store.Book(ctx, b)
}

func (s *Service) ListBookings(ctx context.Context, movieID string) ([]Booking, error) {
	return s.store.ListBookings(ctx, movieID)
}

func (s *Service) ConfirmSeat(ctx context.Context, sessionID, userID string) (Booking, error) {
	return s.store.Confirm(ctx, sessionID, userID)
}

func (s *Service) ReleaseSeat(ctx context.Context, sessionID, userID string) error {
	return s.store.Release(ctx, sessionID, userID)
}
