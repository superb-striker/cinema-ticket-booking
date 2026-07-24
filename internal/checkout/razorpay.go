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
	"net/url"
	"time"
)

type Razorpay struct {
	keyID         string
	keySecret     string
	webhookSecret string
	client        *http.Client
	baseURL       string
}

type RazorpayAPIError struct {
	StatusCode  int
	Code        string
	Description string
	Source      string
	Step        string
	Reason      string
}

func (e *RazorpayAPIError) Error() string {
	message := fmt.Sprintf("razorpay returned HTTP %d", e.StatusCode)
	if e.Code != "" {
		message += " (" + e.Code + ")"
	}
	if e.Description != "" {
		message += ": " + e.Description
	}
	if e.Reason != "" && e.Reason != e.Description {
		message += " [reason=" + e.Reason + "]"
	}
	return message
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

func (r *Razorpay) CapturedPayment(ctx context.Context, providerOrderID string) (PaymentEvent, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		r.baseURL+"/orders/"+url.PathEscape(providerOrderID)+"/payments", nil)
	if err != nil {
		return PaymentEvent{}, false, err
	}
	var response struct {
		Items []struct {
			ID       string `json:"id"`
			OrderID  string `json:"order_id"`
			Status   string `json:"status"`
			Amount   int64  `json:"amount"`
			Currency string `json:"currency"`
		} `json:"items"`
	}
	if err := r.doJSON(req, &response); err != nil {
		return PaymentEvent{}, false, err
	}
	for _, payment := range response.Items {
		if payment.Status != "captured" || payment.OrderID != providerOrderID {
			continue
		}
		raw, _ := json.Marshal(payment)
		return PaymentEvent{ID: "reconcile:" + payment.ID, Type: "payment.captured", ProviderOrderID: payment.OrderID,
			PaymentID: payment.ID, AmountPaise: payment.Amount, Currency: payment.Currency, Raw: raw}, true, nil
	}
	return PaymentEvent{}, false, nil
}

func (r *Razorpay) Refund(ctx context.Context, action RefundAction) (string, error) {
	if id, ok, err := r.findRefund(ctx, action); err != nil {
		return "", err
	} else if ok {
		return id, nil
	}
	body, err := json.Marshal(map[string]any{"amount": action.AmountPaise,
		"notes": map[string]string{"refund_action_id": action.ID.String(), "internal_order_id": action.OrderID.String(), "reason": "late_payment_after_seat_release"}})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+"/payments/"+
		url.PathEscape(action.ProviderPaymentID)+"/refund", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	var response struct {
		ID        string `json:"id"`
		PaymentID string `json:"payment_id"`
		Amount    int64  `json:"amount"`
	}
	if err := r.doJSON(req, &response); err != nil {
		return "", err
	}
	if response.ID == "" || response.PaymentID != action.ProviderPaymentID || response.Amount != action.AmountPaise {
		return "", errors.New("razorpay returned a mismatched refund")
	}
	return response.ID, nil
}

func (r *Razorpay) findRefund(ctx context.Context, action RefundAction) (string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+"/payments/"+
		url.PathEscape(action.ProviderPaymentID)+"/refunds", nil)
	if err != nil {
		return "", false, err
	}
	var response struct {
		Items []struct {
			ID        string            `json:"id"`
			PaymentID string            `json:"payment_id"`
			Amount    int64             `json:"amount"`
			Notes     map[string]string `json:"notes"`
		} `json:"items"`
	}
	if err := r.doJSON(req, &response); err != nil {
		return "", false, err
	}
	for _, refund := range response.Items {
		if refund.Notes["refund_action_id"] == action.ID.String() {
			if refund.PaymentID != action.ProviderPaymentID || refund.Amount != action.AmountPaise {
				return "", false, errors.New("existing Razorpay refund does not match action")
			}
			return refund.ID, true, nil
		}
	}
	return "", false, nil
}

func (r *Razorpay) doJSON(req *http.Request, dst any) error {
	req.SetBasicAuth(r.keyID, r.keySecret)
	req.Header.Set("Accept", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("razorpay request: %w", err)
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, 1<<20)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, readErr := io.ReadAll(limited)
		if readErr != nil {
			return fmt.Errorf("read Razorpay error response: %w", readErr)
		}
		var response struct {
			Error struct {
				Code        string `json:"code"`
				Description string `json:"description"`
				Source      string `json:"source"`
				Step        string `json:"step"`
				Reason      string `json:"reason"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &response)
		return &RazorpayAPIError{StatusCode: resp.StatusCode, Code: response.Error.Code,
			Description: response.Error.Description, Source: response.Error.Source,
			Step: response.Error.Step, Reason: response.Error.Reason}
	}
	if err := json.NewDecoder(limited).Decode(dst); err != nil {
		return fmt.Errorf("decode razorpay response: %w", err)
	}
	return nil
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
