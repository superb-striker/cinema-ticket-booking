package checkout

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestRazorpayWebhookVerificationUsesRawBody(t *testing.T) {
	r := NewRazorpay("key", "api-secret", "webhook-secret")
	body := []byte(`{"event":"payment.captured"}`)
	if !r.VerifyWebhook(body, sign("webhook-secret", body)) {
		t.Fatal("valid signature rejected")
	}
	if r.VerifyWebhook(append(body, ' '), sign("webhook-secret", body)) {
		t.Fatal("modified body accepted")
	}
}

func TestParseCapturedPayment(t *testing.T) {
	body := []byte(`{"event":"payment.captured","payload":{"payment":{"entity":{"id":"pay_1","order_id":"order_1","amount":70000,"currency":"INR","status":"captured"}},"order":{"entity":{"receipt":"44b47a9d-0590-44dc-b99d-5b3eb23464b6"}}}}`)
	event, err := ParseRazorpayEvent("event_1", body)
	if err != nil {
		t.Fatal(err)
	}
	if event.PaymentID != "pay_1" || event.ProviderOrderID != "order_1" || event.AmountPaise != 70000 || event.ReceiptID == "" {
		t.Fatalf("unexpected event: %+v", event)
	}
}

func TestPaymentCallbackSignature(t *testing.T) {
	r := NewRazorpay("key", "api-secret", "webhook-secret")
	payload := []byte("order_1|pay_1")
	if !r.VerifyPaymentSignature("order_1", "pay_1", sign("api-secret", payload)) {
		t.Fatal("valid callback signature rejected")
	}
}

func TestCapturedPaymentFetchesCapturedOrderPayment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/orders/order_1/payments" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"id":"pay_1","order_id":"order_1","status":"captured","amount":70000,"currency":"INR"}]}`))
	}))
	defer server.Close()
	r := NewRazorpay("key", "secret", "webhook")
	r.baseURL = server.URL
	event, ok, err := r.CapturedPayment(t.Context(), "order_1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || event.PaymentID != "pay_1" || event.AmountPaise != 70000 {
		t.Fatalf("unexpected event: %+v %v", event, ok)
	}
}

func TestRefundReusesExistingReceipt(t *testing.T) {
	posts := 0
	action := RefundAction{ID: uuid.MustParse("44b47a9d-0590-44dc-b99d-5b3eb23464b6"), OrderID: uuid.New(), ProviderPaymentID: "pay_1", AmountPaise: 70000}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"id":"rfnd_1","payment_id":"pay_1","notes":{"refund_action_id":"44b47a9d-0590-44dc-b99d-5b3eb23464b6"},"amount":70000}]}`))
	}))
	defer server.Close()
	r := NewRazorpay("key", "secret", "webhook")
	r.baseURL = server.URL
	id, err := r.Refund(t.Context(), action)
	if err != nil {
		t.Fatal(err)
	}
	if id != "rfnd_1" || posts != 0 {
		t.Fatalf("id=%q posts=%d", id, posts)
	}
}

func TestRazorpayErrorPreservesProviderDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"BAD_REQUEST_ERROR","description":"refund is not available","source":"business","step":"refund_initiation","reason":"insufficient_balance"}}`))
	}))
	defer server.Close()
	r := NewRazorpay("key", "secret", "webhook")
	r.baseURL = server.URL

	_, _, err := r.CapturedPayment(t.Context(), "order_1")
	var providerErr *RazorpayAPIError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected RazorpayAPIError, got %T: %v", err, err)
	}
	if providerErr.StatusCode != http.StatusBadRequest || providerErr.Code != "BAD_REQUEST_ERROR" ||
		providerErr.Description != "refund is not available" || providerErr.Source != "business" ||
		providerErr.Step != "refund_initiation" || providerErr.Reason != "insufficient_balance" {
		t.Fatalf("unexpected provider error: %+v", providerErr)
	}
}
