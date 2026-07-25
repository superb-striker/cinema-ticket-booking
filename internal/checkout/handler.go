package checkout

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/superb-striker/cinema/internal/auth"
	"github.com/superb-striker/cinema/internal/utils"
)

type Handler struct {
	service  *Service
	repo     Repository
	razorpay *Razorpay
}

func NewHandler(service *Service, repo Repository, razorpay *Razorpay) *Handler {
	return &Handler{service: service, repo: repo, razorpay: razorpay}
}

func parsePathUUID(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid "+name)
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) ListScreenings(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter := ScreeningFilter{Query: strings.TrimSpace(query.Get("q")), City: strings.TrimSpace(query.Get("city")),
		Date: strings.TrimSpace(query.Get("date")), Language: strings.TrimSpace(query.Get("language")), Format: strings.TrimSpace(query.Get("format"))}
	if value := strings.TrimSpace(query.Get("cinema_id")); value != "" {
		id, err := uuid.Parse(value)
		if err != nil {
			utils.WriteError(w, http.StatusBadRequest, "invalid cinema_id")
			return
		}
		filter.CinemaID = id
	}
	if filter.Date != "" {
		if _, err := time.Parse("2006-01-02", filter.Date); err != nil {
			utils.WriteError(w, http.StatusBadRequest, "date must use YYYY-MM-DD")
			return
		}
	}
	items, err := h.service.ListScreenings(r.Context(), filter)
	if err != nil {
		serverError(w, err)
		return
	}
	if items == nil {
		items = []Screening{}
	}
	utils.WriteJSON(w, http.StatusOK, items)
}

func (h *Handler) Seats(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathUUID(w, r, "screeningID")
	if !ok {
		return
	}
	items, err := h.service.Seats(r.Context(), id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if items == nil {
		items = []Seat{}
	}
	utils.WriteJSON(w, http.StatusOK, items)
}

func (h *Handler) CreateHold(w http.ResponseWriter, r *http.Request) {
	screeningID, ok := parsePathUUID(w, r, "screeningID")
	if !ok {
		return
	}
	user, _ := auth.UserFromContext(r.Context())
	var body struct {
		SeatIDs []string `json:"seat_ids"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	hold, err := h.service.Hold(r.Context(), screeningID, user.ID, body.SeatIDs)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	utils.WriteJSON(w, http.StatusCreated, hold)
}

func (h *Handler) Checkout(w http.ResponseWriter, r *http.Request) {
	screeningID, ok := parsePathUUID(w, r, "screeningID")
	if !ok {
		return
	}
	holdID, ok := parsePathUUID(w, r, "holdID")
	if !ok {
		return
	}
	user, _ := auth.UserFromContext(r.Context())
	order, err := h.service.Checkout(r.Context(), screeningID, holdID, user.ID, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	utils.WriteJSON(w, http.StatusCreated, map[string]any{"order": order, "checkout_key": h.razorpay.PublicKey()})
}

func (h *Handler) Order(w http.ResponseWriter, r *http.Request) {
	orderID, ok := parsePathUUID(w, r, "orderID")
	if !ok {
		return
	}
	user, _ := auth.UserFromContext(r.Context())
	order, err := h.repo.Order(r.Context(), orderID, user.ID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	utils.WriteJSON(w, http.StatusOK, order)
}

func (h *Handler) VerifyPayment(w http.ResponseWriter, r *http.Request) {
	orderID, ok := parsePathUUID(w, r, "orderID")
	if !ok {
		return
	}
	user, _ := auth.UserFromContext(r.Context())
	order, err := h.repo.Order(r.Context(), orderID, user.ID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	var body struct {
		OrderID   string `json:"razorpay_order_id"`
		PaymentID string `json:"razorpay_payment_id"`
		Signature string `json:"razorpay_signature"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if order.ProviderOrderID == "" || body.OrderID != order.ProviderOrderID ||
		!h.razorpay.VerifyPaymentSignature(order.ProviderOrderID, body.PaymentID, body.Signature) {
		utils.WriteError(w, http.StatusBadRequest, "invalid payment signature")
		return
	}
	// The browser callback proves authenticity, but only the captured-payment
	// webhook changes inventory to confirmed. Clients poll the order endpoint.
	utils.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "payment_verification_accepted"})
}

func (h *Handler) Webhook(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if !h.razorpay.VerifyWebhook(body, r.Header.Get("X-Razorpay-Signature")) {
		utils.WriteError(w, http.StatusUnauthorized, "invalid webhook signature")
		return
	}
	event, err := ParseRazorpayEvent(r.Header.Get("X-Razorpay-Event-Id"), body)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid webhook event")
		return
	}
	if _, err = h.repo.ConfirmPayment(r.Context(), event); err != nil {
		serverError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		utils.WriteError(w, http.StatusBadRequest, "request body must contain one JSON object")
		return false
	}
	return true
}

func writeDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidSeat):
		utils.WriteError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrScreeningClosed), errors.Is(err, ErrHoldNotFound):
		utils.WriteError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrSeatsUnavailable), errors.Is(err, ErrIdempotencyReuse):
		utils.WriteError(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrForbidden):
		utils.WriteError(w, http.StatusForbidden, err.Error())
	case strings.Contains(err.Error(), "Idempotency-Key"):
		utils.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		serverError(w, err)
	}
}

func serverError(w http.ResponseWriter, err error) {
	log.Printf("internal error: %v", err)
	utils.WriteError(w, http.StatusInternalServerError, "internal server error")
}
