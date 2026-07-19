package checkout

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Razorpay struct {
	keyID         string
	keySecret     string
	webhookSecret string
	client        *http.Client
	baseURL       string
}

func NewRazorpay(keyID, keySecret, webhookSecret string) *Razorpay {
	return &Razorpay{keyID: keyID, keySecret: keySecret, webhookSecret: webhookSecret,
		client: &http.Client{Timeout: 8 * time.Second}, baseURL: "https://api.razorpay.com/v1"}
}

func (r *Razorpay) PublicKey() string { return r.keyID }

func (r *Razorpay) CreateOrder(ctx context.Context, order Order) (PaymentOrder, error) {
	body, err := json.Marshal(map[string]any{
		"amount": order.AmountPaise, "currency": order.Currency, "receipt": order.ID.String(),
		"notes": map[string]string{"internal_order_id": order.ID.String(), "screening_id": order.ScreeningID.String()},
	})
	if err != nil {
		return PaymentOrder{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+"/orders", bytes.NewReader(body))
	if err != nil {
		return PaymentOrder{}, err
	}
	req.SetBasicAuth(r.keyID, r.keySecret)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return PaymentOrder{}, fmt.Errorf("razorpay create order: %w", err)
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, 1<<20)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, limited)
		return PaymentOrder{}, fmt.Errorf("razorpay create order returned HTTP %d", resp.StatusCode)
	}
	var result struct {
		ID       string `json:"id"`
		Amount   int64  `json:"amount"`
		Currency string `json:"currency"`
	}
	if err := json.NewDecoder(limited).Decode(&result); err != nil {
		return PaymentOrder{}, err
	}
	if result.ID == "" {
		return PaymentOrder{}, errors.New("razorpay returned an empty order id")
	}
	return PaymentOrder{ID: result.ID, Amount: result.Amount, Currency: result.Currency}, nil
}

func (r *Razorpay) VerifyWebhook(body []byte, signature string) bool {
	provided, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(r.webhookSecret))
	_, _ = mac.Write(body)
	return hmac.Equal(provided, mac.Sum(nil))
}

func (r *Razorpay) VerifyPaymentSignature(providerOrderID, paymentID, signature string) bool {
	provided, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(r.keySecret))
	_, _ = mac.Write([]byte(providerOrderID + "|" + paymentID))
	return hmac.Equal(provided, mac.Sum(nil))
}

func ParseRazorpayEvent(eventID string, body []byte) (PaymentEvent, error) {
	var envelope struct {
		Event   string `json:"event"`
		Payload struct {
			Payment struct {
				Entity struct {
					ID       string `json:"id"`
					OrderID  string `json:"order_id"`
					Amount   int64  `json:"amount"`
					Currency string `json:"currency"`
					Status   string `json:"status"`
				} `json:"entity"`
			} `json:"payment"`
			Order struct {
				Entity struct {
					Receipt string `json:"receipt"`
				} `json:"entity"`
			} `json:"order"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return PaymentEvent{}, err
	}
	if eventID == "" || envelope.Event == "" {
		return PaymentEvent{}, errors.New("invalid webhook envelope")
	}
	p := envelope.Payload.Payment.Entity
	if envelope.Event == "payment.captured" && (p.ID == "" || p.OrderID == "" || p.Status != "captured") {
		return PaymentEvent{}, errors.New("invalid captured payment event")
	}
	return PaymentEvent{ID: eventID, Type: envelope.Event, ProviderOrderID: p.OrderID,
		PaymentID: p.ID, AmountPaise: p.Amount, Currency: p.Currency,
		ReceiptID: envelope.Payload.Order.Entity.Receipt, Raw: body}, nil
}
