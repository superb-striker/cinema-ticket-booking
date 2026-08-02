package checkout

import (
	"context"
	"errors"
	"fmt"
	"time"

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

func (p *PostgresRepository) ListScreenings(ctx context.Context, filter ScreeningFilter) ([]Screening, error) {
	rows, err := p.db.Query(ctx,
		`SELECT sc.id, sc.movie_id, m.title, c.id, c.name, c.city, sc.screen_id, s.name, s.format,
		sc.starts_at, sc.sales_end_at, sc.base_price_paise, sc.currency
		FROM screenings sc JOIN movies m ON m.id=sc.movie_id JOIN screens s ON s.id=sc.screen_id
		JOIN cinemas c ON c.id=s.cinema_id
		WHERE sc.status='scheduled' AND sc.sales_end_at > now()
		  AND m.active AND s.active AND c.active
		  AND ($1::text='' OR m.title ILIKE '%' || $1 || '%')
		  AND ($2::uuid='00000000-0000-0000-0000-000000000000' OR c.id=$2)
		  AND ($3::text='' OR lower(c.city)=lower($3))
		  AND ($4::text='' OR (sc.starts_at >= (($4::date)::timestamp AT TIME ZONE c.timezone)
		       AND sc.starts_at < ((($4::date + 1)::date)::timestamp AT TIME ZONE c.timezone)))
		  AND ($5::text='' OR lower(m.language)=lower($5))
		  AND ($6::text='' OR lower(s.format)=lower($6))
		ORDER BY sc.starts_at`, filter.Query, filter.CinemaID, filter.City, filter.Date, filter.Language, filter.Format,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Screening
	for rows.Next() {
		var v Screening
		if err := rows.Scan(&v.ID, &v.MovieID, &v.MovieTitle, &v.CinemaID, &v.CinemaName, &v.CinemaCity, &v.ScreenID, &v.ScreenName, &v.ScreenFormat,
			&v.StartsAt, &v.SalesEndAt, &v.BasePricePaise, &v.Currency); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

func scanScreening(row pgx.Row) (Screening, error) {
	var v Screening
	err := row.Scan(&v.ID, &v.MovieID, &v.MovieTitle, &v.CinemaID, &v.CinemaName, &v.CinemaCity, &v.ScreenID, &v.ScreenName, &v.ScreenFormat,
		&v.StartsAt, &v.SalesEndAt, &v.BasePricePaise, &v.Currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return Screening{}, ErrScreeningClosed
	}
	return v, err
}

func (p *PostgresRepository) Screening(ctx context.Context, id uuid.UUID) (Screening, error) {
	return scanScreening(p.db.QueryRow(ctx, `SELECT sc.id, sc.movie_id, m.title, c.id, c.name, c.city, sc.screen_id, s.name, s.format,
		sc.starts_at, sc.sales_end_at, sc.base_price_paise, sc.currency
		FROM screenings sc JOIN movies m ON m.id=sc.movie_id JOIN screens s ON s.id=sc.screen_id
		JOIN cinemas c ON c.id=s.cinema_id
		WHERE sc.id=$1 AND sc.status='scheduled' AND sc.sales_end_at > now()
		  AND m.active AND s.active AND c.active`, id,
	))
}

func (p *PostgresRepository) Seats(ctx context.Context, screeningID uuid.UUID) ([]Seat, error) {
	if err := p.expireOrders(ctx); err != nil {
		return nil, err
	}
	rows, err := p.db.Query(ctx, `SELECT st.id, st.row_label, st.seat_number, st.category, st.accessible,
		CASE WHEN sb.id IS NOT NULL THEN 'blocked'
		     WHEN os.status='confirmed' THEN 'confirmed'
		     WHEN os.status='payment_pending' THEN 'payment_pending' 
		     ELSE 'available' 
		END
		FROM screenings sc JOIN seats st ON st.screen_id=sc.screen_id
		LEFT JOIN order_seats os ON os.screening_id=sc.id
		AND os.seat_id=st.id
		AND os.status IN ('payment_pending','confirmed')
		LEFT JOIN seat_blocks sb ON sb.screen_id=sc.screen_id AND sb.seat_id=st.id
		AND (sb.screening_id IS NULL OR sb.screening_id=sc.id)
		AND sb.released_at IS NULL AND (sb.expires_at IS NULL OR sb.expires_at>now())
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
		subtotal_paise, convenience_fee_paise, tax_paise,discount_paise,COALESCE(promotion_code,''),
		cancellation_cutoff_minutes,cancellation_refund_percent,COALESCE(cancellation_refund_amount_paise,0),
		provider, COALESCE(provider_order_id,''), COALESCE(provider_payment_id,''), expires_at
		FROM orders WHERE user_id=$1 AND idempotency_key=$2`, hold.UserID, idempotencyKey).
		Scan(&existing.ID, &existing.ScreeningID, &existing.UserID, &existing.HoldID, &existing.Status,
			&existing.AmountPaise, &existing.Currency, &existing.SubtotalPaise, &existing.ConvenienceFeePaise,
			&existing.TaxPaise, &existing.DiscountPaise, &existing.PromotionCode, &existing.CancellationCutoffMinutes, &existing.CancellationRefundPercent,
			&existing.CancellationRefundAmountPaise, &existing.Provider, &existing.ProviderOrderID,
			&existing.ProviderPaymentID, &existing.ExpiresAt)
	if err == nil {
		if existing.HoldID != hold.ID || existing.PromotionCode != hold.CouponCode {
			return Order{}, false, ErrIdempotencyReuse
		}
		existing.SeatIDs, err = orderSeats(ctx, tx, existing.ID)
		return existing, false, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Order{}, false, err
	}

	var cancellationCutoff, cancellationRefundPercent int
	var currency string
	err = tx.QueryRow(ctx, `SELECT sc.currency,
		COALESCE(cp.cutoff_minutes,120),COALESCE(cp.refund_percent,100)
		FROM screenings sc JOIN screens s ON s.id=sc.screen_id
		LEFT JOIN cinema_cancellation_policies cp ON cp.cinema_id=s.cinema_id
		WHERE sc.id=$1 AND sc.status='scheduled' AND sc.sales_end_at>now() FOR UPDATE OF sc`, hold.ScreeningID).
		Scan(&currency, &cancellationCutoff, &cancellationRefundPercent)
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, false, ErrScreeningClosed
	}
	if err != nil {
		return Order{}, false, err
	}

	rows, err := tx.Query(ctx, `SELECT st.id,COALESCE(sp.price_paise,sc.base_price_paise)
		FROM screenings sc
		JOIN screens s ON s.id=sc.screen_id
		JOIN seats st ON st.screen_id=sc.screen_id
		LEFT JOIN pricing_tiers pt ON pt.cinema_id=s.cinema_id
			AND pt.seat_category=st.category AND pt.active
		LEFT JOIN screening_prices sp ON sp.screening_id=sc.id AND sp.pricing_tier_id=pt.id
		WHERE sc.id=$1 AND st.id=ANY($2)`, hold.ScreeningID, hold.SeatIDs)
	if err != nil {
		return Order{}, false, err
	}
	seatPrices := make(map[string]int64, len(hold.SeatIDs))
	var subtotal int64
	for rows.Next() {
		var seatID string
		var seatPrice int64
		if err := rows.Scan(&seatID, &seatPrice); err != nil {
			rows.Close()
			return Order{}, false, err
		}
		seatPrices[seatID] = seatPrice
		subtotal += seatPrice
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Order{}, false, err
	}
	rows.Close()
	if len(seatPrices) != len(hold.SeatIDs) {
		return Order{}, false, ErrInvalidSeat
	}
	var blocked bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM screenings sc JOIN seat_blocks sb ON sb.screen_id=sc.screen_id
		WHERE sc.id=$1 AND sb.seat_id=ANY($2)
		  AND (sb.screening_id IS NULL OR sb.screening_id=sc.id)
		  AND sb.released_at IS NULL AND (sb.expires_at IS NULL OR sb.expires_at>now()))`,
		hold.ScreeningID, hold.SeatIDs).Scan(&blocked); err != nil {
		return Order{}, false, err
	}
	if blocked {
		return Order{}, false, ErrSeatsUnavailable
	}
	var promotionID *uuid.UUID
	var promotionCode *string
	var discount int64
	if hold.CouponCode != "" {
		var id uuid.UUID
		var code, discountType string
		var discountValue, minimum int64
		var maximum *int64
		var maximumRedemptions *int
		var perCustomer int
		err = tx.QueryRow(ctx, `SELECT p.id,p.code,p.discount_type,p.discount_value,
			p.minimum_subtotal_paise,p.maximum_discount_paise,p.maximum_redemptions,p.per_customer_limit
			FROM promotions p JOIN screenings sc ON sc.id=$2 JOIN screens s ON s.id=sc.screen_id
			WHERE p.code=$1 AND p.active AND now()>=p.starts_at AND now()<p.ends_at
			  AND (p.cinema_id IS NULL OR p.cinema_id=s.cinema_id) FOR UPDATE OF p`,
			hold.CouponCode, hold.ScreeningID).Scan(&id, &code, &discountType, &discountValue,
			&minimum, &maximum, &maximumRedemptions, &perCustomer)
		if errors.Is(err, pgx.ErrNoRows) || subtotal < minimum {
			return Order{}, false, ErrInvalidPromotion
		}
		if err != nil {
			return Order{}, false, err
		}
		var totalUses, customerUses int
		if err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE cr.user_id=$2)
			FROM coupon_redemptions cr JOIN orders o ON o.id=cr.order_id
			WHERE cr.promotion_id=$1 AND o.status IN ('payment_pending','confirmed','refund_pending')`,
			id, hold.UserID).Scan(&totalUses, &customerUses); err != nil {
			return Order{}, false, err
		}
		if (maximumRedemptions != nil && totalUses >= *maximumRedemptions) || customerUses >= perCustomer {
			return Order{}, false, ErrInvalidPromotion
		}
		if discountType == "percentage" {
			discount = subtotal * discountValue / 100
		} else {
			discount = discountValue
		}
		if maximum != nil && discount > *maximum {
			discount = *maximum
		}
		if discount >= subtotal {
			discount = subtotal - 1
		}
		promotionID, promotionCode = &id, &code
	}
	convenienceFee := ((subtotal-discount)*300 + 9999) / 10000
	tax := (convenienceFee*1800 + 9999) / 10000
	order := Order{
		ID: uuid.New(), UserID: hold.UserID, ScreeningID: hold.ScreeningID, HoldID: hold.ID,
		SeatIDs: hold.SeatIDs, Status: "payment_pending", SubtotalPaise: subtotal,
		ConvenienceFeePaise: convenienceFee, TaxPaise: tax, DiscountPaise: discount,
		AmountPaise:               subtotal + convenienceFee + tax - discount,
		CancellationCutoffMinutes: cancellationCutoff, CancellationRefundPercent: cancellationRefundPercent,
		Currency: currency, Provider: "razorpay", ExpiresAt: hold.ExpiresAt,
	}
	if promotionCode != nil {
		order.PromotionCode = *promotionCode
	}
	_, err = tx.Exec(ctx, `INSERT INTO orders
		(id,user_id,screening_id,hold_id,idempotency_key,status,amount_paise,currency,provider,expires_at,
		 subtotal_paise,convenience_fee_paise,tax_paise,cancellation_cutoff_minutes,cancellation_refund_percent,
		 promotion_id,promotion_code,discount_paise)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`, order.ID, order.UserID, order.ScreeningID,
		order.HoldID, idempotencyKey, order.Status, order.AmountPaise, order.Currency, order.Provider, order.ExpiresAt,
		order.SubtotalPaise, order.ConvenienceFeePaise, order.TaxPaise, order.CancellationCutoffMinutes,
		order.CancellationRefundPercent, promotionID, promotionCode, discount)
	if err != nil {
		return Order{}, false, mapInventoryError(err)
	}
	if promotionID != nil {
		if _, err = tx.Exec(ctx, `INSERT INTO coupon_redemptions(promotion_id,order_id,user_id,discount_paise)
			VALUES($1,$2,$3,$4)`, *promotionID, order.ID, order.UserID, discount); err != nil {
			return Order{}, false, err
		}
	}
	for _, seatID := range hold.SeatIDs {
		_, err = tx.Exec(ctx, `INSERT INTO order_seats(order_id,screening_id,seat_id,status,price_paise)
			VALUES($1,$2,$3,'payment_pending',$4)`, order.ID, order.ScreeningID, seatID, seatPrices[seatID])
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
	err := p.db.QueryRow(ctx, `SELECT id,screening_id,user_id,hold_id,status,amount_paise,currency,
		subtotal_paise,convenience_fee_paise,tax_paise,discount_paise,COALESCE(promotion_code,''),cancellation_cutoff_minutes,
		cancellation_refund_percent,COALESCE(cancellation_refund_amount_paise,0),provider,
		COALESCE(provider_order_id,''),COALESCE(provider_payment_id,''),expires_at
		FROM orders WHERE id=$1 AND user_id=$2`, orderID, userID).Scan(&order.ID, &order.ScreeningID, &order.UserID,
		&order.HoldID, &order.Status, &order.AmountPaise, &order.Currency, &order.SubtotalPaise,
		&order.ConvenienceFeePaise, &order.TaxPaise, &order.DiscountPaise, &order.PromotionCode, &order.CancellationCutoffMinutes,
		&order.CancellationRefundPercent, &order.CancellationRefundAmountPaise, &order.Provider, &order.ProviderOrderID,
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

func (p *PostgresRepository) Bookings(ctx context.Context, userID uuid.UUID, limit int) ([]Booking, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := p.db.Query(ctx, bookingQuery+` WHERE o.user_id=$1 ORDER BY o.created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Booking
	for rows.Next() {
		booking, err := scanBooking(rows)
		if err != nil {
			return nil, err
		}
		booking.SeatIDs, err = p.bookingSeats(ctx, booking.ID)
		if err != nil {
			return nil, err
		}
		result = append(result, booking)
	}
	return result, rows.Err()
}

func (p *PostgresRepository) Booking(ctx context.Context, orderID, userID uuid.UUID) (Booking, error) {
	booking, err := scanBooking(p.db.QueryRow(ctx, bookingQuery+` WHERE o.id=$1 AND o.user_id=$2`, orderID, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Booking{}, ErrForbidden
	}
	if err != nil {
		return Booking{}, err
	}
	booking.SeatIDs, err = p.bookingSeats(ctx, orderID)
	return booking, err
}

const bookingQuery = `SELECT o.id,o.screening_id,o.user_id,o.hold_id,o.status,o.amount_paise,o.currency,
	o.subtotal_paise,o.convenience_fee_paise,o.tax_paise,o.discount_paise,COALESCE(o.promotion_code,''),o.cancellation_cutoff_minutes,
	o.cancellation_refund_percent,COALESCE(o.cancellation_refund_amount_paise,0),o.provider,
	COALESCE(o.provider_order_id,''),COALESCE(o.provider_payment_id,''),o.expires_at,
	m.title,c.name,c.city,c.timezone,s.name,s.format,sc.starts_at,o.created_at,
	(o.status='confirmed' AND sc.starts_at > now()+(o.cancellation_cutoff_minutes || ' minutes')::interval) AS can_cancel
	FROM orders o JOIN screenings sc ON sc.id=o.screening_id
	JOIN movies m ON m.id=sc.movie_id JOIN screens s ON s.id=sc.screen_id JOIN cinemas c ON c.id=s.cinema_id`

type rowScanner interface{ Scan(...any) error }

func scanBooking(row rowScanner) (Booking, error) {
	var b Booking
	err := row.Scan(&b.ID, &b.ScreeningID, &b.UserID, &b.HoldID, &b.Status, &b.AmountPaise, &b.Currency,
		&b.SubtotalPaise, &b.ConvenienceFeePaise, &b.TaxPaise, &b.DiscountPaise, &b.PromotionCode, &b.CancellationCutoffMinutes,
		&b.CancellationRefundPercent, &b.CancellationRefundAmountPaise, &b.Provider, &b.ProviderOrderID,
		&b.ProviderPaymentID, &b.ExpiresAt, &b.MovieTitle, &b.CinemaName, &b.CinemaCity, &b.CinemaTimezone, &b.ScreenName,
		&b.ScreenFormat, &b.StartsAt, &b.CreatedAt, &b.CanCancel)
	return b, err
}

func (p *PostgresRepository) bookingSeats(ctx context.Context, orderID uuid.UUID) ([]string, error) {
	rows, err := p.db.Query(ctx, `SELECT seat_id FROM order_seats WHERE order_id=$1 ORDER BY seat_id`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var seats []string
	for rows.Next() {
		var seat string
		if err := rows.Scan(&seat); err != nil {
			return nil, err
		}
		seats = append(seats, seat)
	}
	return seats, rows.Err()
}

func (p *PostgresRepository) CancelOrder(ctx context.Context, orderID, userID uuid.UUID, reason string) (Booking, error) {
	tx, err := p.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Booking{}, err
	}
	defer tx.Rollback(ctx)
	var status string
	var startsAt time.Time
	var cutoffMinutes, refundPercent int
	var amountPaise int64
	err = tx.QueryRow(ctx, `SELECT o.status,sc.starts_at,o.cancellation_cutoff_minutes,
		o.cancellation_refund_percent,o.amount_paise FROM orders o JOIN screenings sc ON sc.id=o.screening_id
		WHERE o.id=$1 AND o.user_id=$2 FOR UPDATE OF o`, orderID, userID).
		Scan(&status, &startsAt, &cutoffMinutes, &refundPercent, &amountPaise)
	if errors.Is(err, pgx.ErrNoRows) {
		return Booking{}, ErrForbidden
	}
	if err != nil {
		return Booking{}, err
	}
	if status != "confirmed" || !startsAt.After(time.Now().Add(time.Duration(cutoffMinutes)*time.Minute)) {
		return Booking{}, ErrNotCancellable
	}
	refundAmount := amountPaise * int64(refundPercent) / 100
	if _, err = tx.Exec(ctx, `UPDATE orders SET status='refund_pending',cancellation_requested_at=now(),
		cancellation_reason=$2,cancellation_refund_amount_paise=$3,updated_at=now() WHERE id=$1`,
		orderID, reason, refundAmount); err != nil {
		return Booking{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE order_seats SET status='released' WHERE order_id=$1 AND status='confirmed'`, orderID); err != nil {
		return Booking{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO payment_actions(order_id,action) VALUES($1,'refund') ON CONFLICT DO NOTHING`, orderID); err != nil {
		return Booking{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Booking{}, err
	}
	return p.Booking(ctx, orderID, userID)
}

func (p *PostgresRepository) AdmitTicket(ctx context.Context, orderID uuid.UUID) (time.Time, error) {
	tx, err := p.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return time.Time{}, err
	}
	defer tx.Rollback(ctx)
	var valid bool
	err = tx.QueryRow(ctx, `SELECT o.status='confirmed' AND now() >= sc.starts_at-interval '2 hours'
		AND now() <= sc.starts_at+(m.duration_minutes || ' minutes')::interval
		FROM orders o JOIN screenings sc ON sc.id=o.screening_id JOIN movies m ON m.id=sc.movie_id
		WHERE o.id=$1 FOR UPDATE OF o`, orderID).Scan(&valid)
	if errors.Is(err, pgx.ErrNoRows) || !valid {
		return time.Time{}, ErrTicketInvalid
	}
	if err != nil {
		return time.Time{}, err
	}
	var admittedAt time.Time
	err = tx.QueryRow(ctx, `INSERT INTO ticket_scans(order_id,result) VALUES($1,'admitted')
		ON CONFLICT (order_id) WHERE result='admitted' DO NOTHING RETURNING scanned_at`, orderID).Scan(&admittedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = tx.QueryRow(ctx, `SELECT scanned_at FROM ticket_scans WHERE order_id=$1 AND result='admitted'`, orderID).Scan(&admittedAt)
		_, _ = tx.Exec(ctx, `INSERT INTO ticket_scans(order_id,result) VALUES($1,'duplicate')`, orderID)
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return time.Time{}, commitErr
		}
		return admittedAt, ErrTicketUsed
	}
	if err != nil {
		return time.Time{}, err
	}
	return admittedAt, tx.Commit(ctx)
}

func (p *PostgresRepository) expireOrders(ctx context.Context) error {
	_, err := p.ExpireOrders(ctx)
	return err
}

func (p *PostgresRepository) ExpireOrders(ctx context.Context) (int64, error) {
	tag, err := p.db.Exec(ctx, `WITH expired AS (
		UPDATE orders SET status='expired',updated_at=now()
		WHERE status='payment_pending' AND expires_at <= now() RETURNING id)
		UPDATE order_seats SET status='released' WHERE order_id IN (SELECT id FROM expired)`)
	return tag.RowsAffected(), err
}

func (p *PostgresRepository) ClaimRefund(ctx context.Context, lease time.Duration) (RefundAction, bool, error) {
	var action RefundAction
	err := p.db.QueryRow(ctx, `WITH candidate AS (
		SELECT pa.id FROM payment_actions pa
		WHERE pa.action='refund' AND pa.status IN ('pending','failed')
		  AND pa.next_attempt_at <= now()
		  AND (pa.locked_until IS NULL OR pa.locked_until < now())
		ORDER BY pa.next_attempt_at, pa.created_at
		FOR UPDATE SKIP LOCKED LIMIT 1
	), claimed AS (
		UPDATE payment_actions pa SET status='processing', attempts=attempts+1,
			locked_until=now()+$1::interval, updated_at=now()
		FROM candidate c WHERE pa.id=c.id
		RETURNING pa.id,pa.order_id,pa.attempts
	)
	SELECT c.id,c.order_id,o.provider_payment_id,COALESCE(o.cancellation_refund_amount_paise,o.amount_paise),c.attempts,
		COALESCE(o.cancellation_reason,'late payment after seat release')
	FROM claimed c JOIN orders o ON o.id=c.order_id`, durationInterval(lease)).Scan(
		&action.ID, &action.OrderID, &action.ProviderPaymentID, &action.AmountPaise, &action.Attempts, &action.Reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return RefundAction{}, false, nil
	}
	return action, err == nil, err
}

func (p *PostgresRepository) CompleteRefund(ctx context.Context, action RefundAction, providerRefundID string) error {
	tx, err := p.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE payment_actions SET status='completed',provider_action_id=$2,
		locked_until=NULL,last_error=NULL,updated_at=now() WHERE id=$1 AND status='processing'`, action.ID, providerRefundID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("refund action lease was lost")
	}
	if _, err = tx.Exec(ctx, `UPDATE orders SET status='refunded',updated_at=now() WHERE id=$1 AND status='refund_pending'`, action.OrderID); err != nil {
		return err
	}
	var userID uuid.UUID
	var email string
	var phone *string
	var emailVerified, phoneVerified bool
	if err = tx.QueryRow(ctx, `SELECT u.id,u.email,u.phone,u.email_verified_at IS NOT NULL,u.phone_verified_at IS NOT NULL
		FROM orders o JOIN users u ON u.id=o.user_id WHERE o.id=$1`, action.OrderID).
		Scan(&userID, &email, &phone, &emailVerified, &phoneVerified); err != nil {
		return err
	}
	payload := fmt.Sprintf(`{"order_id":%q,"amount_paise":%q}`, action.OrderID.String(), fmt.Sprint(action.AmountPaise))
	if emailVerified {
		if _, err = tx.Exec(ctx, `INSERT INTO notification_jobs(user_id,order_id,channel,recipient,template,payload)
			VALUES($1,$2,'email',$3,'refund_completed',$4::jsonb) ON CONFLICT DO NOTHING`, userID, action.OrderID, email, payload); err != nil {
			return err
		}
	}
	if phoneVerified && phone != nil && *phone != "" {
		if _, err = tx.Exec(ctx, `INSERT INTO notification_jobs(user_id,order_id,channel,recipient,template,payload)
			VALUES($1,$2,'sms',$3,'refund_completed',$4::jsonb) ON CONFLICT DO NOTHING`, userID, action.OrderID, *phone, payload); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (p *PostgresRepository) RetryRefund(ctx context.Context, action RefundAction, message string, next time.Time) error {
	_, err := p.db.Exec(ctx, `UPDATE payment_actions SET status='failed',locked_until=NULL,last_error=$2,
		next_attempt_at=$3,updated_at=now() WHERE id=$1 AND status='processing'`, action.ID, message, next)
	return err
}

func (p *PostgresRepository) ClaimReconciliation(ctx context.Context, lease time.Duration) (Order, bool, error) {
	var order Order
	err := p.db.QueryRow(ctx, `WITH candidate AS (
		SELECT id FROM orders WHERE provider_order_id IS NOT NULL
		  AND status IN ('payment_pending','expired','payment_failed')
		  AND created_at > now()-interval '7 days' AND next_reconcile_at <= now()
		  AND (reconcile_locked_until IS NULL OR reconcile_locked_until < now())
		ORDER BY next_reconcile_at,created_at FOR UPDATE SKIP LOCKED LIMIT 1
	), claimed AS (
		UPDATE orders o SET reconciliation_attempts=reconciliation_attempts+1,
			reconcile_locked_until=now()+$1::interval,updated_at=now()
		FROM candidate c WHERE o.id=c.id RETURNING o.*)
	SELECT id,screening_id,user_id,hold_id,status,amount_paise,currency,provider,
		provider_order_id,COALESCE(provider_payment_id,''),expires_at,reconciliation_attempts
	FROM claimed`, durationInterval(lease)).Scan(&order.ID, &order.ScreeningID, &order.UserID, &order.HoldID,
		&order.Status, &order.AmountPaise, &order.Currency, &order.Provider, &order.ProviderOrderID,
		&order.ProviderPaymentID, &order.ExpiresAt, &order.ReconciliationAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, false, nil
	}
	return order, err == nil, err
}

func (p *PostgresRepository) CompleteReconciliation(ctx context.Context, order Order) error {
	_, err := p.db.Exec(ctx, `UPDATE orders SET reconcile_locked_until=NULL,next_reconcile_at=now()+interval '7 days'
		WHERE id=$1`, order.ID)
	return err
}

func (p *PostgresRepository) RetryReconciliation(ctx context.Context, order Order, next time.Time) error {
	_, err := p.db.Exec(ctx, `UPDATE orders SET reconcile_locked_until=NULL,next_reconcile_at=$2 WHERE id=$1`, order.ID, next)
	return err
}

func durationInterval(d time.Duration) string { return fmt.Sprintf("%f seconds", d.Seconds()) }

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
	var userID uuid.UUID
	var email string
	var phone *string
	var emailVerified, phoneVerified bool
	if err = tx.QueryRow(ctx, `SELECT u.id,u.email,u.phone,u.email_verified_at IS NOT NULL,u.phone_verified_at IS NOT NULL
		FROM orders o JOIN users u ON u.id=o.user_id WHERE o.id=$1`, orderID).
		Scan(&userID, &email, &phone, &emailVerified, &phoneVerified); err != nil {
		return false, err
	}
	payload := fmt.Sprintf(`{"order_id":%q}`, orderID.String())
	if emailVerified {
		if _, err = tx.Exec(ctx, `INSERT INTO notification_jobs(user_id,order_id,channel,recipient,template,payload)
			VALUES($1,$2,'email',$3,'booking_confirmed',$4::jsonb) ON CONFLICT DO NOTHING`, userID, orderID, email, payload); err != nil {
			return false, err
		}
	}
	if phoneVerified && phone != nil && *phone != "" {
		if _, err = tx.Exec(ctx, `INSERT INTO notification_jobs(user_id,order_id,channel,recipient,template,payload)
			VALUES($1,$2,'sms',$3,'booking_confirmed',$4::jsonb) ON CONFLICT DO NOTHING`, userID, orderID, *phone, payload); err != nil {
			return false, err
		}
	}
	return true, tx.Commit(ctx)
}
