package checkout

import (
	"context"
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
