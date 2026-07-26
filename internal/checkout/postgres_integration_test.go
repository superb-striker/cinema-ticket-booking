package checkout

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresLatePaymentQueuesAndCompletesRefund(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	repo := NewPostgresRepository(db)
	userID, movieID, cinemaID, screenID, screeningID, orderID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.Nil
	_, err = db.Exec(ctx, `INSERT INTO users(id,email,password_hash) VALUES($1,$2,'integration-test')`, userID, userID.String()+"@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO movies(id,slug,title,duration_minutes,language,certificate)
		VALUES($1,$2,'Integration Test',90,'English','U')`, movieID, "integration-"+movieID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO cinemas(id,name,timezone,address_line,city,region,postal_code)
		VALUES($1,$2,'Asia/Kolkata','Test Road','Test City','Test Region','000000')`, cinemaID, "Integration "+cinemaID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO screens(id,cinema_id,name) VALUES($1,$2,$3)`, screenID, cinemaID, "Integration "+screenID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO seats(screen_id,id,row_label,seat_number)
		VALUES($1,'A1','A',1),($1,'A2','A',2)`, screenID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO screenings(id,movie_id,screen_id,starts_at,sales_end_at,base_price_paise,currency)
		VALUES($1,$2,$3,now()+interval '1 day',now()+interval '23 hours',35000,'INR')`, screeningID, movieID, screenID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup := func(query string, args ...any) {
			if _, cleanupErr := db.Exec(context.Background(), query, args...); cleanupErr != nil {
				t.Errorf("cleanup: %v", cleanupErr)
			}
		}
		if orderID != uuid.Nil {
			cleanup(`DELETE FROM notification_jobs WHERE order_id=$1`, orderID)
			cleanup(`DELETE FROM payment_events WHERE provider_order_id=$1`, "order_integration_"+orderID.String())
			cleanup(`DELETE FROM payment_actions WHERE order_id=$1`, orderID)
			cleanup(`DELETE FROM order_seats WHERE order_id=$1`, orderID)
			cleanup(`DELETE FROM orders WHERE id=$1`, orderID)
		}
		cleanup(`DELETE FROM screenings WHERE id=$1`, screeningID)
		cleanup(`DELETE FROM seats WHERE screen_id=$1`, screenID)
		cleanup(`DELETE FROM screens WHERE id=$1`, screenID)
		cleanup(`DELETE FROM cinemas WHERE id=$1`, cinemaID)
		cleanup(`DELETE FROM movies WHERE id=$1`, movieID)
		cleanup(`DELETE FROM users WHERE id=$1`, userID)
	})
	hold := Hold{ID: uuid.New(), ScreeningID: screeningID,
		UserID: userID, SeatIDs: []string{"A1", "A2"}, ExpiresAt: time.Now().Add(10 * time.Minute)}
	order, created, err := repo.CreateOrder(ctx, hold, "integration-"+uuid.NewString())
	if err != nil || !created {
		t.Fatalf("create order: created=%v err=%v", created, err)
	}
	orderID = order.ID
	providerOrderID := "order_integration_" + order.ID.String()
	if err := repo.SetProviderOrder(ctx, order.ID, providerOrderID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE orders SET expires_at=now()-interval '1 minute' WHERE id=$1`, order.ID); err != nil {
		t.Fatal(err)
	}
	if released, err := repo.ExpireOrders(ctx); err != nil || released != 2 {
		t.Fatalf("expire: released=%d err=%v", released, err)
	}
	event := PaymentEvent{ID: "event_integration_" + order.ID.String(), Type: "payment.captured",
		ProviderOrderID: providerOrderID, PaymentID: "pay_integration_" + order.ID.String(),
		AmountPaise: order.AmountPaise, Currency: order.Currency, Raw: []byte(`{"source":"integration"}`)}
	if confirmed, err := repo.ConfirmPayment(ctx, event); err != nil || confirmed {
		t.Fatalf("late payment: confirmed=%v err=%v", confirmed, err)
	}
	action, ok, err := repo.ClaimRefund(ctx, 30*time.Second)
	if err != nil || !ok || action.OrderID != order.ID {
		t.Fatalf("claim refund: action=%+v ok=%v err=%v", action, ok, err)
	}
	if _, ok, err := repo.ClaimRefund(ctx, 30*time.Second); err != nil || ok {
		t.Fatalf("refund was claimed twice: ok=%v err=%v", ok, err)
	}
	if err := repo.CompleteRefund(ctx, action, "rfnd_integration_"+order.ID.String()); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := db.QueryRow(ctx, `SELECT status FROM orders WHERE id=$1`, order.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "refunded" {
		t.Fatalf("want refunded, got %q", status)
	}
}

func TestPostgresCancellationReleasesSeatAndQueuesRefund(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	repo := NewPostgresRepository(db)
	userID, orderID := uuid.New(), uuid.New()
	var screeningID uuid.UUID
	var seatID string
	var price int64
	if err := db.QueryRow(ctx, `SELECT sc.id,st.id,sc.base_price_paise FROM screenings sc
		JOIN seats st ON st.screen_id=sc.screen_id
		WHERE sc.starts_at>now()+interval '3 hours' AND sc.status='scheduled'
		AND NOT EXISTS (SELECT 1 FROM order_seats os WHERE os.screening_id=sc.id AND os.seat_id=st.id
			AND os.status IN ('payment_pending','confirmed')) LIMIT 1`).Scan(&screeningID, &seatID, &price); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO users(id,email,password_hash) VALUES($1,$2,'integration-test')`,
		userID, userID.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	providerPaymentID := "pay_cancel_" + orderID.String()
	if _, err = db.Exec(ctx, `INSERT INTO orders(id,user_id,screening_id,hold_id,idempotency_key,status,
		amount_paise,subtotal_paise,convenience_fee_paise,tax_paise,currency,provider,provider_payment_id,expires_at)
		VALUES($1,$2,$3,$4,$5,'confirmed',$6,$6,0,0,'INR','razorpay',$7,now()+interval '10 minutes')`,
		orderID, userID, screeningID, uuid.New(), uuid.NewString(), price, providerPaymentID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO order_seats(order_id,screening_id,seat_id,status,price_paise)
		VALUES($1,$2,$3,'confirmed',$4)`, orderID, screeningID, seatID, price); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), `DELETE FROM payment_actions WHERE order_id=$1`, orderID)
		_, _ = db.Exec(context.Background(), `DELETE FROM order_seats WHERE order_id=$1`, orderID)
		_, _ = db.Exec(context.Background(), `DELETE FROM orders WHERE id=$1`, orderID)
		_, _ = db.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, userID)
	})
	booking, err := repo.CancelOrder(ctx, orderID, userID, 2*time.Hour, "plans changed")
	if err != nil {
		t.Fatal(err)
	}
	if booking.Status != "refund_pending" || booking.CanCancel {
		t.Fatalf("unexpected cancelled booking: %+v", booking)
	}
	var seatStatus, actionStatus string
	if err := db.QueryRow(ctx, `SELECT os.status,pa.status FROM order_seats os
		JOIN payment_actions pa ON pa.order_id=os.order_id WHERE os.order_id=$1`, orderID).Scan(&seatStatus, &actionStatus); err != nil {
		t.Fatal(err)
	}
	if seatStatus != "released" || actionStatus != "pending" {
		t.Fatalf("seat=%s action=%s", seatStatus, actionStatus)
	}
}

func TestPostgresTicketAdmissionIsSingleUse(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	repo := NewPostgresRepository(db)
	userID, screeningID, orderID := uuid.New(), uuid.New(), uuid.New()
	var movieID, screenID uuid.UUID
	var seatID string
	if err := db.QueryRow(ctx, `SELECT sc.movie_id,sc.screen_id,st.id FROM screenings sc
		JOIN seats st ON st.screen_id=sc.screen_id LIMIT 1`).Scan(&movieID, &screenID, &seatID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO users(id,email,password_hash) VALUES($1,$2,'integration-test')`,
		userID, userID.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO screenings(id,movie_id,screen_id,starts_at,sales_end_at,base_price_paise,currency)
		VALUES($1,$2,$3,now()+interval '30 minutes',now()+interval '20 minutes',10000,'INR')`, screeningID, movieID, screenID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO orders(id,user_id,screening_id,hold_id,idempotency_key,status,
		amount_paise,subtotal_paise,convenience_fee_paise,tax_paise,currency,provider,provider_payment_id,expires_at)
		VALUES($1,$2,$3,$4,$5,'confirmed',10000,10000,0,0,'INR','razorpay',$6,now()+interval '10 minutes')`,
		orderID, userID, screeningID, uuid.New(), uuid.NewString(), "pay_admit_"+orderID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO order_seats(order_id,screening_id,seat_id,status,price_paise)
		VALUES($1,$2,$3,'confirmed',10000)`, orderID, screeningID, seatID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), `DELETE FROM ticket_scans WHERE order_id=$1`, orderID)
		_, _ = db.Exec(context.Background(), `DELETE FROM order_seats WHERE order_id=$1`, orderID)
		_, _ = db.Exec(context.Background(), `DELETE FROM orders WHERE id=$1`, orderID)
		_, _ = db.Exec(context.Background(), `DELETE FROM screenings WHERE id=$1`, screeningID)
		_, _ = db.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, userID)
	})
	first, err := repo.AdmitTicket(ctx, orderID)
	if err != nil || first.IsZero() {
		t.Fatalf("first admission: time=%s err=%v", first, err)
	}
	duplicateTime, err := repo.AdmitTicket(ctx, orderID)
	if !errors.Is(err, ErrTicketUsed) || !duplicateTime.Equal(first) {
		t.Fatalf("duplicate admission: first=%s duplicate=%s err=%v", first, duplicateTime, err)
	}
}
