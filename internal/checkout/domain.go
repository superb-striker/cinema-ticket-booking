package checkout

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidSeat      = errors.New("one or more seats are invalid")
	ErrScreeningClosed  = errors.New("screening is not available for sale")
	ErrSeatsUnavailable = errors.New("one or more seats are unavailable")
	ErrHoldNotFound     = errors.New("hold not found or expired")
	ErrForbidden        = errors.New("hold belongs to another user")
	ErrIdempotencyReuse = errors.New("idempotency key was reused with different parameters")
)

type Screening struct {
	ID             uuid.UUID `json:"id"`
	MovieID        uuid.UUID `json:"movie_id"`
	MovieTitle     string    `json:"movie_title"`
	CinemaID       uuid.UUID `json:"cinema_id"`
	CinemaName     string    `json:"cinema_name"`
	CinemaCity     string    `json:"cinema_city"`
	ScreenID       uuid.UUID `json:"screen_id"`
	ScreenName     string    `json:"screen_name"`
	StartsAt       time.Time `json:"starts_at"`
	SalesEndAt     time.Time `json:"sales_end_at"`
	BasePricePaise int64     `json:"base_price_paise"`
	Currency       string    `json:"currency"`
}

type Seat struct {
	ID         string `json:"id"`
	Row        string `json:"row"`
	Number     int    `json:"number"`
	Category   string `json:"category"`
	Accessible bool   `json:"accessible"`
	Status     string `json:"status"`
}

type Hold struct {
	ID          uuid.UUID `json:"id"`
	ScreeningID uuid.UUID `json:"screening_id"`
	UserID      uuid.UUID `json:"user_id"`
	SeatIDs     []string  `json:"seat_ids"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type Order struct {
	ID                uuid.UUID `json:"id"`
	ScreeningID       uuid.UUID `json:"screening_id"`
	UserID                 uuid.UUID `json:"user_id"`
	HoldID                 uuid.UUID `json:"hold_id"`
	SeatIDs                []string  `json:"seat_ids"`
	Status                 string    `json:"status"`
	AmountPaise            int64     `json:"amount_paise"`
	Currency               string    `json:"currency"`
	Provider               string    `json:"provider"`
	ProviderOrderID        string    `json:"provider_order_id,omitempty"`
	ProviderPaymentID      string    `json:"provider_payment_id,omitempty"`
	ExpiresAt              time.Time `json:"expires_at"`
	ReconciliationAttempts int       `json:"-"`
}

type Repository interface {
	ListScreenings(context.Context) ([]Screening, error)
	Screening(context.Context, uuid.UUID) (Screening, error)
	Seats(context.Context, uuid.UUID) ([]Seat, error)
	ValidateSeats(context.Context, uuid.UUID, []string) (Screening, error)
	CreateOrder(context.Context, Hold, string) (Order, bool, error)
	SetProviderOrder(context.Context, uuid.UUID, string) error
	FailOrder(context.Context, uuid.UUID) error
	Order(context.Context, uuid.UUID, uuid.UUID) (Order, error)
	ConfirmPayment(context.Context, PaymentEvent) (bool, error)
	ExpireOrders(context.Context) (int64, error)
	ClaimRefund(context.Context, time.Duration) (RefundAction, bool, error)
	CompleteRefund(context.Context, RefundAction, string) error
	RetryRefund(context.Context, RefundAction, string, time.Time) error
	ClaimReconciliation(context.Context, time.Duration) (Order, bool, error)
	CompleteReconciliation(context.Context, Order) error
	RetryReconciliation(context.Context, Order, time.Time) error
}

type HoldStore interface {
	Create(context.Context, Hold, time.Duration) (Hold, error)
	Get(context.Context, uuid.UUID, uuid.UUID) (Hold, error)
	Release(context.Context, Hold) error
	HeldSeats(context.Context, uuid.UUID) ([]string, error)
}

type PaymentOrder struct {
	ID       string
	Amount   int64
	Currency string
}

type PaymentProvider interface {
	CreateOrder(context.Context, Order) (PaymentOrder, error)
	CapturedPayment(context.Context, string) (PaymentEvent, bool, error)
	Refund(context.Context, RefundAction) (string, error)
}

type RefundAction struct {
	ID                uuid.UUID
	OrderID           uuid.UUID
	ProviderPaymentID string
	AmountPaise       int64
	Attempts          int
}

type PaymentEvent struct {
	ID              string
	Type            string
	ProviderOrderID string
	PaymentID       string
	AmountPaise     int64
	Currency        string
	ReceiptID       string
	Raw             []byte
}
