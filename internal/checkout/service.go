package checkout

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Service struct {
	repo     Repository
	holds    HoldStore
	payments PaymentProvider
	holdTTL  time.Duration
}

func NewService(repo Repository, holds HoldStore, payments PaymentProvider, holdTTL time.Duration) *Service {
	return &Service{repo: repo, holds: holds, payments: payments, holdTTL: holdTTL}
}

func (s *Service) ListScreenings(ctx context.Context, filter ScreeningFilter) ([]Screening, error) {
	return s.repo.ListScreenings(ctx, filter)
}

func (s *Service) Seats(ctx context.Context, screeningID uuid.UUID) ([]Seat, error) {
	seats, err := s.repo.Seats(ctx, screeningID)
	if err != nil {
		return nil, err
	}
	held, err := s.holds.HeldSeats(ctx, screeningID)
	if err != nil {
		return nil, err
	}
	heldSet := make(map[string]struct{}, len(held))
	for _, id := range held {
		heldSet[id] = struct{}{}
	}
	for i := range seats {
		if seats[i].Status == "available" {
			if _, ok := heldSet[seats[i].ID]; ok {
				seats[i].Status = "held"
			}
		}
	}
	return seats, nil
}

func (s *Service) Hold(ctx context.Context, screeningID, userID uuid.UUID, seatIDs []string) (Hold, error) {
	seatIDs = normalizeSeats(seatIDs)
	if len(seatIDs) == 0 || len(seatIDs) > 10 {
		return Hold{}, ErrInvalidSeat
	}
	if _, err := s.repo.ValidateSeats(ctx, screeningID, seatIDs); err != nil {
		return Hold{}, err
	}
	seats, err := s.repo.Seats(ctx, screeningID)
	if err != nil {
		return Hold{}, err
	}
	unavailable := make(map[string]bool)
	for _, seat := range seats {
		unavailable[seat.ID] = seat.Status != "available"
	}
	for _, id := range seatIDs {
		if unavailable[id] {
			return Hold{}, ErrSeatsUnavailable
		}
	}
	return s.holds.Create(ctx, Hold{ScreeningID: screeningID, UserID: userID, SeatIDs: seatIDs}, s.holdTTL)
}

func normalizeSeats(ids []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.ToUpper(strings.TrimSpace(id))
		if id != "" && !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	sort.Strings(result)
	return result
}

func (s *Service) Checkout(ctx context.Context, screeningID, holdID, userID uuid.UUID, idempotencyKey, couponCode string) (Order, error) {
	if strings.TrimSpace(idempotencyKey) == "" || len(idempotencyKey) > 128 {
		return Order{}, errors.New("a valid Idempotency-Key header is required")
	}
	hold, err := s.holds.Get(ctx, screeningID, holdID)
	if err != nil {
		return Order{}, err
	}
	if hold.UserID != userID {
		return Order{}, ErrForbidden
	}
	hold.CouponCode = strings.ToUpper(strings.TrimSpace(couponCode))
	order, created, err := s.repo.CreateOrder(ctx, hold, idempotencyKey)
	if err != nil {
		return Order{}, err
	}
	if !created || order.ProviderOrderID != "" {
		return order, nil
	}
	providerOrder, err := s.payments.CreateOrder(ctx, order)
	if err != nil {
		// A transport failure is ambiguous: Razorpay may have created the order.
		// Keep the durable reservation until expiry so a later signed webhook can
		// reconcile it by the internal receipt ID without risking double booking.
		return Order{}, err
	}
	if providerOrder.Amount != order.AmountPaise || providerOrder.Currency != order.Currency {
		_ = s.repo.FailOrder(ctx, order.ID)
		return Order{}, errors.New("payment provider returned mismatched order")
	}
	if err := s.repo.SetProviderOrder(ctx, order.ID, providerOrder.ID); err != nil {
		return Order{}, err
	}
	order.ProviderOrderID = providerOrder.ID
	return order, nil
}
