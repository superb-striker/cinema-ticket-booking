package checkout

import (
	"context"
	"log"
	"math"
	"time"
)

type Worker struct {
	repo     WorkerRepository
	payments WorkerPaymentProvider
	interval time.Duration
	lease    time.Duration
	now      func() time.Time
}

type WorkerRepository interface {
	ExpireOrders(context.Context) (int64, error)
	ClaimRefund(context.Context, time.Duration) (RefundAction, bool, error)
	CompleteRefund(context.Context, RefundAction, string) error
	RetryRefund(context.Context, RefundAction, string, time.Time) error
	ClaimReconciliation(context.Context, time.Duration) (Order, bool, error)
	CompleteReconciliation(context.Context, Order) error
	RetryReconciliation(context.Context, Order, time.Time) error
	ConfirmPayment(context.Context, PaymentEvent) (bool, error)
}

type WorkerPaymentProvider interface {
	CapturedPayment(context.Context, string) (PaymentEvent, bool, error)
	Refund(context.Context, RefundAction) (string, error)
}

func NewWorker(repo WorkerRepository, payments WorkerPaymentProvider, interval time.Duration) *Worker {
	return &Worker{repo: repo, payments: payments, interval: interval, lease: 30 * time.Second, now: time.Now}
}

func (w *Worker) Run(ctx context.Context) {
	w.runCycle(ctx)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.runCycle(ctx)
		}
	}
}

func (w *Worker) runCycle(ctx context.Context) {
	cycleCtx, cancel := context.WithTimeout(ctx, w.lease)
	defer cancel()
	if count, err := w.repo.ExpireOrders(cycleCtx); err != nil {
		log.Printf("expire orders: %v", err)
	} else if count > 0 {
		log.Printf("released %d expired order seats", count)
	}
	for range 20 {
		if !w.processRefund(cycleCtx) {
			break
		}
	}
	for range 20 {
		if !w.processReconciliation(cycleCtx) {
			break
		}
	}
}

func (w *Worker) processRefund(ctx context.Context) bool {
	action, ok, err := w.repo.ClaimRefund(ctx, w.lease)
	if err != nil {
		log.Printf("claim refund: %v", err)
		return false
	}
	if !ok {
		return false
	}
	providerID, err := w.payments.Refund(ctx, action)
	if err != nil {
		next := w.now().Add(workerBackoff(action.Attempts, time.Minute, 6*time.Hour))
		if retryErr := w.repo.RetryRefund(ctx, action, err.Error(), next); retryErr != nil {
			log.Printf("retry refund %s: %v", action.ID, retryErr)
		}
		return true
	}
	if err := w.repo.CompleteRefund(ctx, action, providerID); err != nil {
		log.Printf("complete refund %s: %v", action.ID, err)
	}
	return true
}

func (w *Worker) processReconciliation(ctx context.Context) bool {
	order, ok, err := w.repo.ClaimReconciliation(ctx, w.lease)
	if err != nil {
		log.Printf("claim reconciliation: %v", err)
		return false
	}
	if !ok {
		return false
	}
	event, captured, err := w.payments.CapturedPayment(ctx, order.ProviderOrderID)
	if err == nil && captured {
		_, err = w.repo.ConfirmPayment(ctx, event)
	}
	if err != nil {
		next := w.now().Add(workerBackoff(order.ReconciliationAttempts, 30*time.Second, 30*time.Minute))
		if retryErr := w.repo.RetryReconciliation(ctx, order, next); retryErr != nil {
			log.Printf("retry reconciliation %s: %v", order.ID, retryErr)
		}
		return true
	}
	if captured {
		if err := w.repo.CompleteReconciliation(ctx, order); err != nil {
			log.Printf("complete reconciliation %s: %v", order.ID, err)
		}
		return true
	}
	next := w.now().Add(workerBackoff(order.ReconciliationAttempts, 30*time.Second, 30*time.Minute))
	if err := w.repo.RetryReconciliation(ctx, order, next); err != nil {
		log.Printf("reschedule reconciliation %s: %v", order.ID, err)
	}
	return true
}

func workerBackoff(attempt int, base, maximum time.Duration) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	power := math.Pow(2, float64(attempt-1))
	delay := time.Duration(float64(base) * power)
	if delay > maximum || delay < 0 {
		return maximum
	}
	return delay
}
