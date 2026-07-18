package checkout

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	db *pgxpool.Pool
}

func NewPostgresRepository(db *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func (p *PostgresRepository) ListScreenings(ctx context.Context) ([]Screening, error) {
	rows, err := p.db.Query(ctx,
		`SELECT sc.id, sc.movie_id, m.title, sc.screen_id, s.name, 
		sc.starts_at, sc.sales_end_at, sc.base_price_paise, sc.currency
		FROM screenings sc JOIN movies m ON m.id=sc.movie_id JOIN screens s ON s.id=sc.screen_id
		WHERE sc.status='scheduled' AND sc.sales_end_at > now() 
		ORDER BY sc.starts_at`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Screening
	for rows.Next() {
		var v Screening
		if err := rows.Scan(&v.ID, &v.MovieID, &v.MovieTitle, &v.ScreenID, &v.ScreenName,
			&v.StartsAt, &v.SalesEndAt, &v.BasePricePaise, &v.Currency); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

func scanScreening(row pgx.Row) (Screening, error) {
	var v Screening
	err := row.Scan(&v.ID, &v.MovieID, &v.MovieTitle, &v.ScreenID, &v.ScreenName,
		&v.StartsAt, &v.SalesEndAt, &v.BasePricePaise, &v.Currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return Screening{}, ErrScreeningClosed
	}
	return v, err
}

func (p *PostgresRepository) Screening(ctx context.Context, id uuid.UUID) (Screening, error) {
	return scanScreening(p.db.QueryRow(ctx, `SELECT sc.id, sc.movie_id, m.title, sc.screen_id, s.name,
		sc.starts_at, sc.sales_end_at, sc.base_price_paise, sc.currency
		FROM screenings sc JOIN movies m ON m.id=sc.movie_id JOIN screens s ON s.id=sc.screen_id
		WHERE sc.id=$1 AND sc.status='scheduled' AND sc.sales_end_at > now()`, id,
	))
}

func (p *PostgresRepository) Seats(ctx context.Context, screeningID uuid.UUID) ([]Seat, error) {
	if err := p.expireOrders(ctx); err != nil {
		return nil, err
	}
	rows, err := p.db.Query(ctx, `SELECT st.id, st.row_label, st.seat_number, st.category, st.accessible,
		CASE WHEN os.status='confirmed' THEN 'confirmed'
		     WHEN os.status='payment_pending' THEN 'payment_pending' 
		     ELSE 'available' 
		END
		FROM screenings sc JOIN seats st ON st.screen_id=sc.screen_id
		LEFT JOIN order_seats os ON os.screening_id=sc.id
		AND os.seat_id=st.id
		AND os.status IN ('payment_pending','confirmed')
		WHERE sc.id=$1 
		ORDER BY st.row_label, st.seat_number`,
		screeningID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Seat
	for rows.Next() {
		var seat Seat
		if err := rows.Scan(&seat.ID, &seat.Row, &seat.Number, &seat.Category, &seat.Accessible, &seat.Status); err != nil {
			return nil, err
		}
		result = append(result, seat)
	}
	return result, rows.Err()
}

func (p *PostgresRepository) ValidateSeats(ctx context.Context, screeningID uuid.UUID, seatIDs []string) (Screening, error) {
	screening, err := p.Screening(ctx, screeningID)
	if err != nil {
		return Screening{}, err
	}
	var count int
	err = p.db.QueryRow(ctx, `SELECT count(*) FROM seats WHERE screen_id=$1 AND id=ANY($2)`,
		screening.ScreenID, seatIDs).Scan(&count)
	if err != nil {
		return Screening{}, err
	}
	if count != len(seatIDs) {
		return Screening{}, ErrInvalidSeat
	}
	return screening, nil
}

func (p *PostgresRepository) CreateOrder(ctx context.Context, hold Hold, idempotencyKey string) (Order, bool, error) {
	if err := p.expireOrders(ctx); err != nil {
		return Order{}, false, err
	}
	tx, err := p.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Order{}, false, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`,
		hold.UserID.String()+":"+idempotencyKey); err != nil {
		return Order{}, false, err
	}

	var existing Order
	err = tx.QueryRow(ctx, `SELECT id, screening_id, user_id, hold_id, status, amount_paise, currency,
		provider, COALESCE(provider_order_id,''), COALESCE(provider_payment_id,''), expires_at
		FROM orders WHERE user_id=$1 AND idempotency_key=$2`, hold.UserID, idempotencyKey).
		Scan(&existing.ID, &existing.ScreeningID, &existing.UserID, &existing.HoldID, &existing.Status,
			&existing.AmountPaise, &existing.Currency, &existing.Provider, &existing.ProviderOrderID,
			&existing.ProviderPaymentID, &existing.ExpiresAt)
	if err == nil {
		if existing.HoldID != hold.ID {
			return Order{}, false, ErrIdempotencyReuse
		}
		existing.SeatIDs, err = orderSeats(ctx, tx, existing.ID)
		return existing, false, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Order{}, false, err
	}

	var price int64
	var currency string
	err = tx.QueryRow(ctx, `SELECT base_price_paise, currency FROM screenings
		WHERE id=$1 AND status='scheduled' AND sales_end_at > now() FOR UPDATE`, hold.ScreeningID).Scan(&price, &currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, false, ErrScreeningClosed
	}
	if err != nil {
		return Order{}, false, err
	}

	order := Order{
		ID: uuid.New(), UserID: hold.UserID, ScreeningID: hold.ScreeningID, HoldID: hold.ID,
		SeatIDs: hold.SeatIDs, Status: "payment_pending", AmountPaise: price * int64(len(hold.SeatIDs)),
		Currency: currency, Provider: "razorpay", ExpiresAt: hold.ExpiresAt,
	}
	_, err = tx.Exec(ctx, `INSERT INTO orders
		(id,user_id,screening_id,hold_id,idempotency_key,status,amount_paise,currency,provider,expires_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, order.ID, order.UserID, order.ScreeningID,
		order.HoldID, idempotencyKey, order.Status, order.AmountPaise, order.Currency, order.Provider, order.ExpiresAt)
	if err != nil {
		return Order{}, false, mapInventoryError(err)
	}
	for _, seatID := range hold.SeatIDs {
		_, err = tx.Exec(ctx, `INSERT INTO order_seats(order_id,screening_id,seat_id,status,price_paise)
			VALUES($1,$2,$3,'payment_pending',$4)`, order.ID, order.ScreeningID, seatID, price)
		if err != nil {
			return Order{}, false, mapInventoryError(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Order{}, false, mapInventoryError(err)
	}
	return order, true, nil
}

func orderSeats(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT seat_id FROM order_seats WHERE order_id=$1 ORDER BY seat_id`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func mapInventoryError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "order_seats_live_inventory_unique" {
		return ErrSeatsUnavailable
	}
	return err
}

func (p *PostgresRepository) SetProviderOrder(ctx context.Context, orderID uuid.UUID, providerID string) error {
	_, err := p.db.Exec(ctx, `UPDATE orders SET provider_order_id=$2, updated_at=now()
		WHERE id=$1 AND status='payment_pending'`, orderID, providerID)
	return err
}

func (p *PostgresRepository) FailOrder(ctx context.Context, orderID uuid.UUID) error {
	tx, err := p.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE orders SET status='payment_failed',updated_at=now()
		WHERE id=$1 AND status='payment_pending'`, orderID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE order_seats SET status='released'
		WHERE order_id=$1 AND status='payment_pending'`, orderID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *PostgresRepository) Order(ctx context.Context, orderID, userID uuid.UUID) (Order, error) {
	var order Order
	err := p.db.QueryRow(ctx, `SELECT id,screening_id,user_id,hold_id,status,amount_paise,currency,provider,
		COALESCE(provider_order_id,''),COALESCE(provider_payment_id,''),expires_at
		FROM orders WHERE id=$1 AND user_id=$2`, orderID, userID).Scan(&order.ID, &order.ScreeningID, &order.UserID,
		&order.HoldID, &order.Status, &order.AmountPaise, &order.Currency, &order.Provider, &order.ProviderOrderID,
		&order.ProviderPaymentID, &order.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, ErrForbidden
	}
	if err != nil {
		return Order{}, err
	}
	rows, err := p.db.Query(ctx, `SELECT seat_id FROM order_seats WHERE order_id=$1 ORDER BY seat_id`, orderID)
	if err != nil {
		return Order{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return Order{}, err
		}
		order.SeatIDs = append(order.SeatIDs, id)
	}
	return order, rows.Err()
}

func (p *PostgresRepository) expireOrders(ctx context.Context) error {
	_, err := p.db.Exec(ctx, `WITH expired AS (
		UPDATE orders SET status='expired',updated_at=now()
		WHERE status='payment_pending' AND expires_at <= now() RETURNING id)
		UPDATE order_seats SET status='released' WHERE order_id IN (SELECT id FROM expired)`)
	return err
}

func (p *PostgresRepository) ConfirmPayment(ctx context.Context, event PaymentEvent) (bool, error) {
	tx, err := p.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `INSERT INTO payment_events
		(provider,event_id,event_type,provider_order_id,provider_payment_id,payload)
		VALUES('razorpay',$1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, event.ID, event.Type,
		event.ProviderOrderID, event.PaymentID, string(event.Raw))
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if event.Type != "payment.captured" {
		return false, tx.Commit(ctx)
	}

	var orderID uuid.UUID
	var amount int64
	var currency, status string
	err = tx.QueryRow(ctx, `SELECT id,amount_paise,currency,status FROM orders
		WHERE provider_order_id=$1 OR ($2 <> '' AND id::text=$2) FOR UPDATE`, event.ProviderOrderID, event.ReceiptID).Scan(
		&orderID, &amount, &currency, &status)
	if err != nil {
		return false, fmt.Errorf("payment order lookup: %w", err)
	}
	if amount != event.AmountPaise || currency != event.Currency {
		return false, errors.New("payment amount or currency mismatch")
	}
	if status == "confirmed" {
		return false, tx.Commit(ctx)
	}
	if status != "payment_pending" {
		_, err = tx.Exec(ctx, `UPDATE orders SET status='refund_pending',provider_payment_id=$2,updated_at=now() WHERE id=$1`,
			orderID, event.PaymentID)
		if err != nil {
			return false, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO payment_actions(order_id,action) VALUES($1,'refund') ON CONFLICT DO NOTHING`,
			orderID)
		if err != nil {
			return false, err
		}
		return false, tx.Commit(ctx)
	}
	_, err = tx.Exec(ctx, `UPDATE orders SET status='confirmed',provider_payment_id=$2,updated_at=now() WHERE id=$1`,
		orderID, event.PaymentID)
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `UPDATE order_seats SET status='confirmed' WHERE order_id=$1 AND status='payment_pending'`, orderID)
	if err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
