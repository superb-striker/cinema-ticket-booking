package checkout

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type workerRepoFake struct {
	refund          RefundAction
	order           Order
	completedRefund string
	confirmed       PaymentEvent
	refundRetry     time.Time
	reconcileRetry  time.Time
}

func (f *workerRepoFake) ExpireOrders(context.Context) (int64, error) { return 0, nil }
func (f *workerRepoFake) ClaimRefund(context.Context, time.Duration) (RefundAction, bool, error) {
	if f.refund.ID == uuid.Nil {
		return RefundAction{}, false, nil
	}
	v := f.refund
	f.refund = RefundAction{}
	return v, true, nil
}
func (f *workerRepoFake) CompleteRefund(_ context.Context, _ RefundAction, id string) error {
	f.completedRefund = id
	return nil
}
func (f *workerRepoFake) RetryRefund(_ context.Context, _ RefundAction, _ string, next time.Time) error {
	f.refundRetry = next
	return nil
}
func (f *workerRepoFake) ClaimReconciliation(context.Context, time.Duration) (Order, bool, error) {
	if f.order.ID == uuid.Nil {
		return Order{}, false, nil
	}
	v := f.order
	f.order = Order{}
	return v, true, nil
}
func (f *workerRepoFake) CompleteReconciliation(context.Context, Order) error { return nil }
func (f *workerRepoFake) RetryReconciliation(_ context.Context, _ Order, next time.Time) error {
	f.reconcileRetry = next
	return nil
}
func (f *workerRepoFake) ConfirmPayment(_ context.Context, event PaymentEvent) (bool, error) {
	f.confirmed = event
	return true, nil
}

type workerProviderFake struct {
	refundID   string
	refundErr  error
	event      PaymentEvent
	captured   bool
	captureErr error
}

func (f workerProviderFake) Refund(context.Context, RefundAction) (string, error) {
	return f.refundID, f.refundErr
}
func (f workerProviderFake) CapturedPayment(context.Context, string) (PaymentEvent, bool, error) {
	return f.event, f.captured, f.captureErr
}

func TestWorkerCompletesRefund(t *testing.T) {
	repo := &workerRepoFake{refund: RefundAction{ID: uuid.New(), Attempts: 1}}
	worker := NewWorker(repo, workerProviderFake{refundID: "rfnd_1"}, time.Second)
	if !worker.processRefund(context.Background()) {
		t.Fatal("expected work")
	}
	if repo.completedRefund != "rfnd_1" {
		t.Fatalf("refund not completed: %q", repo.completedRefund)
	}
}

func TestWorkerRetriesRefundWithBackoff(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	repo := &workerRepoFake{refund: RefundAction{ID: uuid.New(), Attempts: 3}}
	worker := NewWorker(repo, workerProviderFake{refundErr: errors.New("temporary")}, time.Second)
	worker.now = func() time.Time { return now }
	worker.processRefund(context.Background())
	if want := now.Add(4 * time.Minute); !repo.refundRetry.Equal(want) {
		t.Fatalf("want retry %v got %v", want, repo.refundRetry)
	}
}

func TestWorkerReconcilesCapturedPayment(t *testing.T) {
	event := PaymentEvent{ID: "reconcile:pay_1", PaymentID: "pay_1"}
	repo := &workerRepoFake{order: Order{ID: uuid.New(), ProviderOrderID: "order_1"}}
	worker := NewWorker(repo, workerProviderFake{event: event, captured: true}, time.Second)
	worker.processReconciliation(context.Background())
	if repo.confirmed.PaymentID != "pay_1" {
		t.Fatalf("payment was not confirmed: %+v", repo.confirmed)
	}
}

func TestWorkerBackoffIsCapped(t *testing.T) {
	if got := workerBackoff(99, time.Minute, 6*time.Hour); got != 6*time.Hour {
		t.Fatalf("got %v", got)
	}
}
