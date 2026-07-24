package checkout

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

type paymentCallbackRepository struct {
	Repository
	order Order
}

func (r paymentCallbackRepository) Order(context.Context, uuid.UUID, uuid.UUID) (Order, error) {
	return r.order, nil
}

func TestVerifyPaymentAcceptsRazorpayCallback(t *testing.T) {
	const (
		providerOrderID = "order_1"
		paymentID       = "pay_1"
		secret          = "api-secret"
	)
	orderID := uuid.New()
	repo := paymentCallbackRepository{order: Order{ID: orderID, ProviderOrderID: providerOrderID}}
	handler := NewHandler(nil, repo, NewRazorpay("key", secret, "webhook-secret"))
	signature := sign(secret, []byte(providerOrderID+"|"+paymentID))
	body := fmt.Sprintf(`{"razorpay_payment_id":%q,"razorpay_order_id":%q,"razorpay_signature":%q}`,
		paymentID, providerOrderID, signature)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/orders/"+orderID.String()+"/verify-payment", bytes.NewBufferString(body))
	req.SetPathValue("orderID", orderID.String())
	recorder := httptest.NewRecorder()

	handler.VerifyPayment(recorder, req)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestVerifyPaymentRejectsMismatchedRazorpayOrder(t *testing.T) {
	const (
		providerOrderID = "order_expected"
		callbackOrderID = "order_other"
		paymentID       = "pay_1"
		secret          = "api-secret"
	)
	orderID := uuid.New()
	repo := paymentCallbackRepository{order: Order{ID: orderID, ProviderOrderID: providerOrderID}}
	handler := NewHandler(nil, repo, NewRazorpay("key", secret, "webhook-secret"))
	signature := sign(secret, []byte(callbackOrderID+"|"+paymentID))
	body := fmt.Sprintf(`{"razorpay_payment_id":%q,"razorpay_order_id":%q,"razorpay_signature":%q}`,
		paymentID, callbackOrderID, signature)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/orders/"+orderID.String()+"/verify-payment", bytes.NewBufferString(body))
	req.SetPathValue("orderID", orderID.String())
	recorder := httptest.NewRecorder()

	handler.VerifyPayment(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
