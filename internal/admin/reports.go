package admin

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/superb-striker/cinema/internal/utils"
)

type reportFilter struct {
	From     time.Time
	To       time.Time
	CinemaID any
}

func parseReportFilter(r *http.Request) (reportFilter, error) {
	now := time.Now().UTC()
	from := now.AddDate(0, 0, -30).Truncate(24 * time.Hour)
	to := now.AddDate(0, 0, 1).Truncate(24 * time.Hour)
	var err error
	if value := strings.TrimSpace(r.URL.Query().Get("from")); value != "" {
		from, err = time.Parse("2006-01-02", value)
		if err != nil {
			return reportFilter{}, err
		}
	}
	if value := strings.TrimSpace(r.URL.Query().Get("to")); value != "" {
		to, err = time.Parse("2006-01-02", value)
		if err != nil {
			return reportFilter{}, err
		}
		to = to.AddDate(0, 0, 1)
	}
	if !to.After(from) || to.Sub(from) > 366*24*time.Hour {
		return reportFilter{}, errInvalidReportRange
	}
	cinemaID, err := optionalUUID(r.URL.Query().Get("cinema_id"))
	if err != nil {
		return reportFilter{}, err
	}
	return reportFilter{From: from, To: to, CinemaID: cinemaID}, nil
}

var errInvalidReportRange = &reportRangeError{}

type reportRangeError struct{}

func (*reportRangeError) Error() string { return "report range must contain 1-366 days" }

func reportParams(w http.ResponseWriter, r *http.Request) (reportFilter, bool) {
	filter, err := parseReportFilter(r)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, "from/to must be valid dates spanning no more than 366 days; cinema_id must be a UUID")
		return reportFilter{}, false
	}
	return filter, true
}

func (h *CatalogHandler) SalesReport(w http.ResponseWriter, r *http.Request) {
	f, ok := reportParams(w, r)
	if !ok {
		return
	}
	rows, err := h.db.Query(r.Context(), `SELECT o.created_at::date,
		count(*) FILTER (WHERE o.status IN ('confirmed','refund_pending','refunded')),
		COALESCE(sum(seat_counts.tickets)
			FILTER (WHERE o.status IN ('confirmed','refund_pending','refunded')),0),
		COALESCE(sum(o.subtotal_paise) FILTER (WHERE o.status IN ('confirmed','refund_pending','refunded')),0),
		COALESCE(sum(o.discount_paise) FILTER (WHERE o.status IN ('confirmed','refund_pending','refunded')),0)
		FROM orders o JOIN screenings sc ON sc.id=o.screening_id JOIN screens s ON s.id=sc.screen_id
		LEFT JOIN LATERAL (SELECT count(*) tickets FROM order_seats os WHERE os.order_id=o.id) seat_counts ON true
		WHERE o.created_at>=$1 AND o.created_at<$2 AND ($3::uuid IS NULL OR s.cinema_id=$3)
		GROUP BY o.created_at::date ORDER BY o.created_at::date`, f.From, f.To, f.CinemaID)
	if err != nil {
		serverError(w)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var day time.Time
		var bookings, tickets int64
		var subtotal, discounts int64
		if err := rows.Scan(&day, &bookings, &tickets, &subtotal, &discounts); err != nil {
			serverError(w)
			return
		}
		items = append(items, map[string]any{"date": day.Format("2006-01-02"), "bookings": bookings,
			"tickets": tickets, "subtotal_paise": subtotal, "discount_paise": discounts})
	}
	utils.WriteJSON(w, http.StatusOK, items)
}

func (h *CatalogHandler) OccupancyReport(w http.ResponseWriter, r *http.Request) {
	f, ok := reportParams(w, r)
	if !ok {
		return
	}
	rows, err := h.db.Query(r.Context(), `SELECT sc.id,m.title,c.id,c.name,s.name,sc.starts_at,
		(SELECT count(*) FROM seats st WHERE st.screen_id=sc.screen_id) capacity,
		count(os.seat_id) FILTER (WHERE os.status='confirmed') occupied,
		COALESCE(sum(os.price_paise) FILTER (WHERE os.status='confirmed'),0) ticket_revenue
		FROM screenings sc JOIN movies m ON m.id=sc.movie_id JOIN screens s ON s.id=sc.screen_id
		JOIN cinemas c ON c.id=s.cinema_id LEFT JOIN order_seats os ON os.screening_id=sc.id
		WHERE sc.starts_at>=$1 AND sc.starts_at<$2 AND ($3::uuid IS NULL OR c.id=$3)
		GROUP BY sc.id,m.title,c.id,c.name,s.name ORDER BY sc.starts_at`, f.From, f.To, f.CinemaID)
	if err != nil {
		serverError(w)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var showID, cinemaID uuid.UUID
		var movie, cinema, auditorium string
		var starts time.Time
		var capacity, occupied, revenue int64
		if err := rows.Scan(&showID, &movie, &cinemaID, &cinema, &auditorium, &starts,
			&capacity, &occupied, &revenue); err != nil {
			serverError(w)
			return
		}
		percent := float64(0)
		if capacity > 0 {
			percent = float64(occupied) * 100 / float64(capacity)
		}
		items = append(items, map[string]any{"show_id": showID, "movie_title": movie,
			"cinema_id": cinemaID, "cinema_name": cinema, "auditorium_name": auditorium,
			"starts_at": starts, "capacity": capacity, "occupied": occupied,
			"occupancy_percent": percent, "ticket_revenue_paise": revenue})
	}
	utils.WriteJSON(w, http.StatusOK, items)
}

func (h *CatalogHandler) PaymentFailuresReport(w http.ResponseWriter, r *http.Request) {
	f, ok := reportParams(w, r)
	if !ok {
		return
	}
	rows, err := h.db.Query(r.Context(), `SELECT o.created_at::date,
		count(*) FILTER (WHERE o.status='payment_failed') failed,
		count(*) FILTER (WHERE o.status='expired') expired,
		count(*) FILTER (WHERE o.status IN ('confirmed','refund_pending','refunded')) successful,
		COALESCE(sum(o.amount_paise) FILTER (WHERE o.status IN ('payment_failed','expired')),0) failed_value
		FROM orders o JOIN screenings sc ON sc.id=o.screening_id JOIN screens s ON s.id=sc.screen_id
		WHERE o.created_at>=$1 AND o.created_at<$2 AND ($3::uuid IS NULL OR s.cinema_id=$3)
		GROUP BY o.created_at::date ORDER BY o.created_at::date`, f.From, f.To, f.CinemaID)
	if err != nil {
		serverError(w)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var day time.Time
		var failed, expired, successful, value int64
		if err := rows.Scan(&day, &failed, &expired, &successful, &value); err != nil {
			serverError(w)
			return
		}
		total := failed + expired + successful
		rate := float64(0)
		if total > 0 {
			rate = float64(failed+expired) * 100 / float64(total)
		}
		items = append(items, map[string]any{"date": day.Format("2006-01-02"), "failed": failed,
			"expired": expired, "successful": successful, "failure_percent": rate,
			"failed_value_paise": value})
	}
	utils.WriteJSON(w, http.StatusOK, items)
}

func (h *CatalogHandler) RevenueReport(w http.ResponseWriter, r *http.Request) {
	f, ok := reportParams(w, r)
	if !ok {
		return
	}
	rows, err := h.db.Query(r.Context(), `SELECT c.id,c.name,o.currency,
		count(*) FILTER (WHERE o.status IN ('confirmed','refund_pending','refunded')) bookings,
		COALESCE(sum(o.amount_paise) FILTER (WHERE o.status IN ('confirmed','refund_pending','refunded')),0) gross,
		COALESCE(sum(o.cancellation_refund_amount_paise) FILTER (WHERE o.status IN ('refund_pending','refunded')),0) refunds,
		COALESCE(sum(o.discount_paise) FILTER (WHERE o.status IN ('confirmed','refund_pending','refunded')),0) discounts
		FROM orders o JOIN screenings sc ON sc.id=o.screening_id JOIN screens s ON s.id=sc.screen_id
		JOIN cinemas c ON c.id=s.cinema_id
		WHERE o.created_at>=$1 AND o.created_at<$2 AND ($3::uuid IS NULL OR c.id=$3)
		GROUP BY c.id,c.name,o.currency ORDER BY c.name,o.currency`, f.From, f.To, f.CinemaID)
	if err != nil {
		serverError(w)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var cinemaID uuid.UUID
		var cinema, currency string
		var bookings, gross, refunds, discounts int64
		if err := rows.Scan(&cinemaID, &cinema, &currency, &bookings, &gross, &refunds, &discounts); err != nil {
			serverError(w)
			return
		}
		items = append(items, map[string]any{"cinema_id": cinemaID, "cinema_name": cinema,
			"currency": currency, "bookings": bookings, "gross_paise": gross,
			"refunds_paise": refunds, "discount_paise": discounts, "net_paise": gross - refunds})
	}
	utils.WriteJSON(w, http.StatusOK, items)
}
