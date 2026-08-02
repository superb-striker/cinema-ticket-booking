package admin

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/superb-striker/cinema/internal/auth"
	"github.com/superb-striker/cinema/internal/utils"
)

type SupportBooking struct {
	ID                uuid.UUID `json:"id"`
	UserID            uuid.UUID `json:"user_id"`
	Email             string    `json:"email"`
	MovieTitle        string    `json:"movie_title"`
	CinemaName        string    `json:"cinema_name"`
	ShowStartsAt      time.Time `json:"show_starts_at"`
	Seats             []string  `json:"seats"`
	Status            string    `json:"status"`
	AmountPaise       int64     `json:"amount_paise"`
	Currency          string    `json:"currency"`
	PromotionCode     string    `json:"promotion_code,omitempty"`
	ProviderOrderID   string    `json:"provider_order_id,omitempty"`
	ProviderPaymentID string    `json:"provider_payment_id,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

const supportBookingQuery = `SELECT o.id,o.user_id,u.email,m.title,c.name,sc.starts_at,
	COALESCE((SELECT array_agg(os.seat_id ORDER BY os.seat_id) FROM order_seats os WHERE os.order_id=o.id),'{}'),
	o.status,o.amount_paise,o.currency,COALESCE(o.promotion_code,''),
	COALESCE(o.provider_order_id,''),COALESCE(o.provider_payment_id,''),o.created_at
	FROM orders o JOIN users u ON u.id=o.user_id JOIN screenings sc ON sc.id=o.screening_id
	JOIN movies m ON m.id=sc.movie_id JOIN screens s ON s.id=sc.screen_id JOIN cinemas c ON c.id=s.cinema_id`

func (h *CatalogHandler) SupportBookings(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	rows, err := h.db.Query(r.Context(), supportBookingQuery+`
		WHERE ($1='' OR o.id::text ILIKE '%'||$1||'%' OR u.email ILIKE '%'||$1||'%'
		 OR COALESCE(o.provider_order_id,'') ILIKE '%'||$1||'%'
		 OR COALESCE(o.provider_payment_id,'') ILIKE '%'||$1||'%')
		AND ($2='' OR o.status=$2) ORDER BY o.created_at DESC LIMIT 100`, q, status)
	if err != nil {
		serverError(w)
		return
	}
	defer rows.Close()
	items := []SupportBooking{}
	for rows.Next() {
		booking, err := scanSupportBooking(rows)
		if err != nil {
			serverError(w)
			return
		}
		items = append(items, booking)
	}
	utils.WriteJSON(w, http.StatusOK, items)
}

func (h *CatalogHandler) SupportBooking(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "orderID")
	if !ok {
		return
	}
	booking, err := scanSupportBooking(h.db.QueryRow(r.Context(), supportBookingQuery+` WHERE o.id=$1`, id))
	if err == pgx.ErrNoRows {
		utils.WriteError(w, http.StatusNotFound, "booking not found")
		return
	}
	if err != nil {
		serverError(w)
		return
	}
	utils.WriteJSON(w, http.StatusOK, booking)
}

type supportScanner interface{ Scan(...any) error }

func scanSupportBooking(row supportScanner) (SupportBooking, error) {
	var v SupportBooking
	err := row.Scan(&v.ID, &v.UserID, &v.Email, &v.MovieTitle, &v.CinemaName, &v.ShowStartsAt,
		&v.Seats, &v.Status, &v.AmountPaise, &v.Currency, &v.PromotionCode,
		&v.ProviderOrderID, &v.ProviderPaymentID, &v.CreatedAt)
	return v, err
}

func (h *CatalogHandler) SupportCancel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason        string `json:"reason"`
		RefundPercent *int   `json:"refund_percent,omitempty"`
	}
	if !decode(w, r, &body) {
		return
	}
	body.Reason = strings.TrimSpace(body.Reason)
	if body.Reason == "" || len(body.Reason) > 500 {
		utils.WriteError(w, http.StatusBadRequest, "a cancellation reason is required")
		return
	}
	id, ok := pathID(w, r, "orderID")
	if !ok {
		return
	}
	user, _ := auth.UserFromContext(r.Context())
	if body.RefundPercent != nil && !user.HasRole("admin") {
		utils.WriteError(w, http.StatusForbidden, "only administrators can override refund policy")
		return
	}
	if err := h.cancelBooking(r, id, body.Reason, body.RefundPercent); err != nil {
		if err == pgx.ErrNoRows {
			utils.WriteError(w, http.StatusConflict, "only confirmed bookings can be cancelled")
		} else {
			serverError(w)
		}
		return
	}
	h.audit(r, "booking.cancelled", "order", id.String(), body)
	utils.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

func (h *CatalogHandler) SupportRefund(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "orderID")
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if !decode(w, r, &body) {
		return
	}
	body.Reason = strings.TrimSpace(body.Reason)
	if body.Reason == "" || len(body.Reason) > 500 {
		utils.WriteError(w, http.StatusBadRequest, "a refund reason is required")
		return
	}
	full := 100
	if err := h.cancelBooking(r, id, body.Reason, &full); err != nil {
		if err == pgx.ErrNoRows {
			utils.WriteError(w, http.StatusConflict, "only confirmed bookings can be refunded")
		} else {
			serverError(w)
		}
		return
	}
	h.audit(r, "booking.refund_requested", "order", id.String(), body)
	utils.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "refund_pending"})
}

func (h *CatalogHandler) cancelBooking(r *http.Request, orderID uuid.UUID, reason string, override *int) error {
	tx, err := h.db.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	var amount int64
	var policyPercent int
	err = tx.QueryRow(r.Context(), `SELECT amount_paise,cancellation_refund_percent FROM orders
		WHERE id=$1 AND status='confirmed' FOR UPDATE`, orderID).Scan(&amount, &policyPercent)
	if err != nil {
		return err
	}
	percent := policyPercent
	if override != nil {
		percent = *override
	}
	if percent < 0 || percent > 100 {
		return pgx.ErrNoRows
	}
	refund := amount * int64(percent) / 100
	status := "refund_pending"
	var refundValue any = refund
	if refund == 0 {
		status = "cancelled"
		refundValue = nil
	}
	if _, err = tx.Exec(r.Context(), `UPDATE orders SET status=$2,cancellation_requested_at=now(),
		cancellation_reason=$3,cancellation_refund_amount_paise=$4,updated_at=now() WHERE id=$1`,
		orderID, status, reason, refundValue); err != nil {
		return err
	}
	if _, err = tx.Exec(r.Context(), `UPDATE order_seats SET status='released'
		WHERE order_id=$1 AND status='confirmed'`, orderID); err != nil {
		return err
	}
	if refund > 0 {
		if _, err = tx.Exec(r.Context(), `INSERT INTO payment_actions(order_id,action)
			VALUES($1,'refund') ON CONFLICT DO NOTHING`, orderID); err != nil {
			return err
		}
	}
	return tx.Commit(r.Context())
}

func (h *CatalogHandler) SupportNotes(w http.ResponseWriter, r *http.Request) {
	orderID, ok := pathID(w, r, "orderID")
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		rows, err := h.db.Query(r.Context(), `SELECT n.id,n.note,n.created_at,n.author_user_id,
			COALESCE(u.email,'deleted user') FROM booking_support_notes n
			LEFT JOIN users u ON u.id=n.author_user_id WHERE n.order_id=$1 ORDER BY n.created_at DESC`, orderID)
		if err != nil {
			serverError(w)
			return
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var id uuid.UUID
			var note, email string
			var created time.Time
			var author *uuid.UUID
			if err := rows.Scan(&id, &note, &created, &author, &email); err != nil {
				serverError(w)
				return
			}
			items = append(items, map[string]any{"id": id, "note": note, "created_at": created, "author_user_id": author, "author_email": email})
		}
		utils.WriteJSON(w, http.StatusOK, items)
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	if !decode(w, r, &body) {
		return
	}
	body.Note = strings.TrimSpace(body.Note)
	if body.Note == "" || len(body.Note) > 2000 {
		utils.WriteError(w, http.StatusBadRequest, "note must contain 1-2000 characters")
		return
	}
	user, _ := auth.UserFromContext(r.Context())
	var id uuid.UUID
	err := h.db.QueryRow(r.Context(), `INSERT INTO booking_support_notes(id,order_id,author_user_id,note)
		SELECT gen_random_uuid(),o.id,$2,$3 FROM orders o WHERE o.id=$1 RETURNING id`,
		orderID, user.ID, body.Note).Scan(&id)
	if err == pgx.ErrNoRows {
		utils.WriteError(w, http.StatusNotFound, "booking not found")
		return
	}
	if err != nil {
		serverError(w)
		return
	}
	h.audit(r, "booking.note_added", "order", orderID.String(), map[string]any{"note_id": id})
	utils.WriteJSON(w, http.StatusCreated, map[string]any{"id": id, "note": body.Note})
}
