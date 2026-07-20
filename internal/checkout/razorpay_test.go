package checkout

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
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
