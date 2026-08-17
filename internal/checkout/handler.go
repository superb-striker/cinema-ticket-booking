package checkout

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"text/template"
	"time"
	_ "time/tzdata"

	"github.com/google/uuid"
	qrcode "github.com/skip2/go-qrcode"
	"github.com/superb-striker/cinema/internal/auth"
	"github.com/superb-striker/cinema/internal/utils"
)

type Handler struct {
	service      *Service
	repo         Repository
	razorpay     *Razorpay
	ticketSecret []byte
	scannerKey   string
}

func NewHandler(service *Service, repo Repository, razorpay *Razorpay, ticketSecret, scannerKey string) *Handler {
	return &Handler{service: service, repo: repo, razorpay: razorpay, ticketSecret: []byte(ticketSecret), scannerKey: scannerKey}
}

func (h *Handler) Bookings(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	items, err := h.repo.Bookings(r.Context(), user.ID, 50)
	if err != nil {
		serverError(w, err)
		return
	}
	if items == nil {
		items = []Booking{}
	}
	utils.WriteJSON(w, http.StatusOK, items)
}

func (h *Handler) CancelBooking(w http.ResponseWriter, r *http.Request) {
	orderID, ok := parsePathUUID(w, r, "orderID")
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	body.Reason = strings.TrimSpace(body.Reason)
	if body.Reason == "" || len(body.Reason) > 200 {
		utils.WriteError(w, http.StatusBadRequest, "a cancellation reason is required")
		return
	}
	user, _ := auth.UserFromContext(r.Context())
	booking, err := h.repo.CancelOrder(r.Context(), orderID, user.ID, body.Reason)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	utils.WriteJSON(w, http.StatusAccepted, booking)
}

func (h *Handler) DownloadTicket(w http.ResponseWriter, r *http.Request) {
	orderID, ok := parsePathUUID(w, r, "orderID")
	if !ok {
		return
	}
	user, _ := auth.UserFromContext(r.Context())
	booking, err := h.repo.Booking(r.Context(), orderID, user.ID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if booking.Status != "confirmed" {
		utils.WriteError(w, http.StatusConflict, "a ticket is available only for confirmed bookings")
		return
	}
	code := h.ticketCode(orderID)
	png, err := qrcode.Encode(code, qrcode.Medium, 256)
	if err != nil {
		serverError(w, err)
		return
	}
	data := struct {
		Booking  Booking
		QR       string
		StartsAt string
	}{Booking: booking, QR: base64.StdEncoding.EncodeToString(png), StartsAt: formatCinemaTime(booking.StartsAt, booking.CinemaTimezone)}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="ticket-%s.html"`, orderID))
	if err := ticketTemplate.Execute(w, data); err != nil {
		slog.ErrorContext(r.Context(), "render ticket", "error", err)
	}
}

func (h *Handler) ValidateTicket(w http.ResponseWriter, r *http.Request) {
	if h.scannerKey == "" || !hmac.Equal([]byte(r.Header.Get("X-Scanner-Key")), []byte(h.scannerKey)) {
		utils.WriteError(w, http.StatusUnauthorized, "scanner authentication required")
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	orderID, ok := h.parseTicketCode(body.Code)
	if !ok {
		utils.WriteError(w, http.StatusBadRequest, ErrTicketInvalid.Error())
		return
	}
	admittedAt, err := h.repo.AdmitTicket(r.Context(), orderID)
	if err != nil {
		if errors.Is(err, ErrTicketUsed) {
			utils.WriteJSON(w, http.StatusConflict, map[string]any{"valid": false, "error": err.Error(), "first_admitted_at": admittedAt})
			return
		}
		writeDomainError(w, err)
		return
	}
	utils.WriteJSON(w, http.StatusOK, map[string]any{"valid": true, "order_id": orderID, "admitted_at": admittedAt})
}

func (h *Handler) ticketCode(orderID uuid.UUID) string {
	payload := orderID.String()
	mac := hmac.New(sha256.New, h.ticketSecret)
	_, _ = mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (h *Handler) parseTicketCode(code string) (uuid.UUID, bool) {
	parts := strings.Split(code, ".")
	if len(parts) != 2 {
		return uuid.Nil, false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return uuid.Nil, false
	}
	mac := hmac.New(sha256.New, h.ticketSecret)
	_, _ = mac.Write([]byte(parts[0]))
	id, err := uuid.Parse(parts[0])
	return id, err == nil && hmac.Equal(signature, mac.Sum(nil))
}

var ticketTemplate = template.Must(template.New("ticket").Parse(`<!doctype html><html><head><meta charset="utf-8"><title>Ticket {{.Booking.ID}}</title><style>body{font:16px system-ui;max-width:680px;margin:40px auto;padding:24px;color:#111}.ticket{border:2px solid #111;border-radius:16px;padding:28px}img{width:256px;max-width:100%}.muted{color:#555}@media print{body{margin:0}.ticket{border:0}}</style></head><body><main class="ticket"><h1>{{.Booking.MovieTitle}}</h1><p>{{.Booking.CinemaName}}, {{.Booking.CinemaCity}} · {{.Booking.ScreenName}} · {{.Booking.ScreenFormat}}</p><p><strong>{{.StartsAt}}</strong></p><p>Seats: <strong>{{range $i,$s := .Booking.SeatIDs}}{{if $i}}, {{end}}{{$s}}{{end}}</strong></p><img src="data:image/png;base64,{{.QR}}" alt="Admission QR code"><p class="muted">Booking {{.Booking.ID}} · Keep this code private. It admits the ticket once.</p></main></body></html>`))

func formatCinemaTime(value time.Time, timezone string) string {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		location = time.UTC
	}
	return value.In(location).Format("Mon, 02 Jan 2006 at 03:04 PM MST")
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

func (h *Handler) SeatEvents(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathUUID(w, r, "screeningID")
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		utils.WriteError(w, http.StatusInternalServerError, "streaming is unavailable")
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")

	var previous []byte
	send := func() error {
		seats, err := h.service.Seats(r.Context(), id)
		if err != nil {
			return err
		}
		payload, err := json.Marshal(seats)
		if err != nil {
			return err
		}
		if bytes.Equal(payload, previous) {
			return nil
		}
		previous = append(previous[:0], payload...)
		_, err = fmt.Fprintf(w, "event: seats\ndata: %s\n\n", payload)
		flusher.Flush()
		return err
	}
	if err := send(); err != nil {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	heartbeat := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if err := send(); err != nil {
				return
			}
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
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
	var body struct {
		CouponCode string `json:"coupon_code"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &body) {
		return
	}
	order, err := h.service.Checkout(r.Context(), screeningID, holdID, user.ID, r.Header.Get("Idempotency-Key"), body.CouponCode)
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
	case errors.Is(err, ErrInvalidPromotion):
		utils.WriteError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, ErrNotCancellable), errors.Is(err, ErrTicketUsed):
		utils.WriteError(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrTicketInvalid):
		utils.WriteError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, ErrForbidden):
		utils.WriteError(w, http.StatusForbidden, err.Error())
	case strings.Contains(err.Error(), "Idempotency-Key"):
		utils.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		serverError(w, err)
	}
}

func serverError(w http.ResponseWriter, err error) {
	slog.Error("checkout handler failed", "error", err)
	utils.WriteError(w, http.StatusInternalServerError, "internal server error")
}
