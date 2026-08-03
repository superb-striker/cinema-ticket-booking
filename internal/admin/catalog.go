package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/superb-striker/cinema/internal/auth"
	"github.com/superb-striker/cinema/internal/utils"
)

type CatalogHandler struct{ db *pgxpool.Pool }

func NewCatalogHandler(db *pgxpool.Pool) *CatalogHandler { return &CatalogHandler{db: db} }

type Movie struct {
	ID              uuid.UUID `json:"id"`
	Slug            string    `json:"slug"`
	Title           string    `json:"title"`
	DurationMinutes int       `json:"duration_minutes"`
	Language        string    `json:"language"`
	Certificate     string    `json:"certificate"`
	Active          bool      `json:"active"`
}

type Cinema struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	Timezone      string    `json:"timezone"`
	AddressLine   string    `json:"address_line"`
	City          string    `json:"city"`
	Region        string    `json:"region"`
	PostalCode    string    `json:"postal_code"`
	Active        bool      `json:"active"`
	CutoffMinutes int       `json:"cancellation_cutoff_minutes"`
	RefundPercent int       `json:"cancellation_refund_percent"`
}

type Auditorium struct {
	ID       uuid.UUID `json:"id"`
	CinemaID uuid.UUID `json:"cinema_id"`
	Name     string    `json:"name"`
	Format   string    `json:"format"`
	Active   bool      `json:"active"`
}

type SeatInput struct {
	ID         string `json:"id"`
	Row        string `json:"row"`
	Number     int    `json:"number"`
	Category   string `json:"category"`
	Accessible bool   `json:"accessible"`
}

type PricingTier struct {
	ID           uuid.UUID `json:"id"`
	CinemaID     uuid.UUID `json:"cinema_id"`
	Name         string    `json:"name"`
	SeatCategory string    `json:"seat_category"`
	Active       bool      `json:"active"`
}

type ShowInput struct {
	MovieID        uuid.UUID   `json:"movie_id"`
	AuditoriumID   uuid.UUID   `json:"auditorium_id"`
	StartsAt       time.Time   `json:"starts_at"`
	SalesEndAt     time.Time   `json:"sales_end_at"`
	BasePricePaise int64       `json:"base_price_paise"`
	Currency       string      `json:"currency"`
	Status         string      `json:"status"`
	Prices         []ShowPrice `json:"prices"`
}

type ShowPrice struct {
	PricingTierID uuid.UUID `json:"pricing_tier_id"`
	PricePaise    int64     `json:"price_paise"`
}

func (h *CatalogHandler) Movies(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rows, err := h.db.Query(r.Context(), `SELECT id,slug,title,duration_minutes,language,certificate,active FROM movies ORDER BY title`)
		if err != nil {
			serverError(w)
			return
		}
		defer rows.Close()
		items := []Movie{}
		for rows.Next() {
			var v Movie
			if err := rows.Scan(&v.ID, &v.Slug, &v.Title, &v.DurationMinutes, &v.Language, &v.Certificate, &v.Active); err != nil {
				serverError(w)
				return
			}
			items = append(items, v)
		}
		utils.WriteJSON(w, http.StatusOK, items)
	case http.MethodPost:
		var v Movie
		if !decode(w, r, &v) {
			return
		}
		if !validMovie(v) {
			utils.WriteError(w, http.StatusBadRequest, "invalid movie")
			return
		}
		v.ID = uuid.New()
		v.Slug = strings.ToLower(strings.TrimSpace(v.Slug))
		v.Active = true
		_, err := h.db.Exec(r.Context(), `INSERT INTO movies(id,slug,title,duration_minutes,language,certificate,active) VALUES($1,$2,$3,$4,$5,$6,true)`, v.ID, v.Slug, strings.TrimSpace(v.Title), v.DurationMinutes, strings.TrimSpace(v.Language), strings.TrimSpace(v.Certificate))
		if err != nil {
			conflictOrServer(w, err)
			return
		}
		h.audit(r, "movie.created", "movie", v.ID.String(), v)
		utils.WriteJSON(w, http.StatusCreated, v)
	}
}

func (h *CatalogHandler) Movie(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "movieID")
	if !ok {
		return
	}
	if r.Method == http.MethodDelete {
		_, err := h.db.Exec(r.Context(), `UPDATE movies SET active=false WHERE id=$1`, id)
		if err != nil {
			serverError(w)
			return
		}
		h.audit(r, "movie.archived", "movie", id.String(), nil)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var v Movie
	if !decode(w, r, &v) {
		return
	}
	if !validMovie(v) {
		utils.WriteError(w, http.StatusBadRequest, "invalid movie")
		return
	}
	_, err := h.db.Exec(r.Context(), `UPDATE movies SET slug=$2,title=$3,duration_minutes=$4,language=$5,certificate=$6,active=$7 WHERE id=$1`, id, strings.ToLower(strings.TrimSpace(v.Slug)), strings.TrimSpace(v.Title), v.DurationMinutes, strings.TrimSpace(v.Language), strings.TrimSpace(v.Certificate), v.Active)
	if err != nil {
		conflictOrServer(w, err)
		return
	}
	v.ID = id
	h.audit(r, "movie.updated", "movie", id.String(), v)
	utils.WriteJSON(w, http.StatusOK, v)
}

func validMovie(v Movie) bool {
	return strings.TrimSpace(v.Slug) != "" && strings.TrimSpace(v.Title) != "" && v.DurationMinutes > 0 && v.DurationMinutes <= 600 && strings.TrimSpace(v.Language) != "" && strings.TrimSpace(v.Certificate) != ""
}

func (h *CatalogHandler) Cinemas(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		rows, err := h.db.Query(r.Context(), `SELECT c.id,c.name,c.timezone,c.address_line,c.city,c.region,c.postal_code,c.active,COALESCE(p.cutoff_minutes,120),COALESCE(p.refund_percent,100) FROM cinemas c LEFT JOIN cinema_cancellation_policies p ON p.cinema_id=c.id ORDER BY c.name`)
		if err != nil {
			serverError(w)
			return
		}
		defer rows.Close()
		items := []Cinema{}
		for rows.Next() {
			var v Cinema
			if err := rows.Scan(&v.ID, &v.Name, &v.Timezone, &v.AddressLine, &v.City, &v.Region, &v.PostalCode, &v.Active, &v.CutoffMinutes, &v.RefundPercent); err != nil {
				serverError(w)
				return
			}
			items = append(items, v)
		}
		utils.WriteJSON(w, http.StatusOK, items)
		return
	}
	var v Cinema
	if !decode(w, r, &v) {
		return
	}
	if !validCinema(v) {
		utils.WriteError(w, http.StatusBadRequest, "invalid cinema")
		return
	}
	v.ID = uuid.New()
	v.Active = true
	tx, err := h.db.Begin(r.Context())
	if err != nil {
		serverError(w)
		return
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), `INSERT INTO cinemas(id,name,timezone,address_line,city,region,postal_code,active) VALUES($1,$2,$3,$4,$5,$6,$7,true)`, v.ID, strings.TrimSpace(v.Name), v.Timezone, strings.TrimSpace(v.AddressLine), strings.TrimSpace(v.City), strings.TrimSpace(v.Region), strings.TrimSpace(v.PostalCode))
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO cinema_cancellation_policies(cinema_id,cutoff_minutes,refund_percent) VALUES($1,$2,$3)`, v.ID, v.CutoffMinutes, v.RefundPercent)
	}
	if err != nil {
		conflictOrServer(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w)
		return
	}
	h.audit(r, "cinema.created", "cinema", v.ID.String(), v)
	utils.WriteJSON(w, http.StatusCreated, v)
}

func (h *CatalogHandler) Cinema(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "cinemaID")
	if !ok {
		return
	}
	if r.Method == http.MethodDelete {
		_, err := h.db.Exec(r.Context(), `UPDATE cinemas SET active=false WHERE id=$1`, id)
		if err != nil {
			serverError(w)
			return
		}
		h.audit(r, "cinema.archived", "cinema", id.String(), nil)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var v Cinema
	if !decode(w, r, &v) {
		return
	}
	if !validCinema(v) {
		utils.WriteError(w, http.StatusBadRequest, "invalid cinema")
		return
	}
	tx, err := h.db.Begin(r.Context())
	if err != nil {
		serverError(w)
		return
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), `UPDATE cinemas SET name=$2,timezone=$3,address_line=$4,city=$5,region=$6,postal_code=$7,active=$8 WHERE id=$1`, id, strings.TrimSpace(v.Name), v.Timezone, strings.TrimSpace(v.AddressLine), strings.TrimSpace(v.City), strings.TrimSpace(v.Region), strings.TrimSpace(v.PostalCode), v.Active)
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO cinema_cancellation_policies(cinema_id,cutoff_minutes,refund_percent,updated_at) VALUES($1,$2,$3,now()) ON CONFLICT(cinema_id) DO UPDATE SET cutoff_minutes=EXCLUDED.cutoff_minutes,refund_percent=EXCLUDED.refund_percent,updated_at=now()`, id, v.CutoffMinutes, v.RefundPercent)
	}
	if err != nil {
		conflictOrServer(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w)
		return
	}
	v.ID = id
	h.audit(r, "cinema.updated", "cinema", id.String(), v)
	utils.WriteJSON(w, http.StatusOK, v)
}

func validCinema(v Cinema) bool {
	if strings.TrimSpace(v.Name) == "" || strings.TrimSpace(v.AddressLine) == "" || strings.TrimSpace(v.City) == "" || strings.TrimSpace(v.Region) == "" || strings.TrimSpace(v.PostalCode) == "" || v.CutoffMinutes < 0 || v.CutoffMinutes > 10080 || v.RefundPercent < 1 || v.RefundPercent > 100 {
		return false
	}
	_, err := time.LoadLocation(v.Timezone)
	return err == nil
}

func (h *CatalogHandler) Auditoriums(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		rows, err := h.db.Query(r.Context(), `SELECT id,cinema_id,name,format,active FROM screens ORDER BY cinema_id,name`)
		if err != nil {
			serverError(w)
			return
		}
		defer rows.Close()
		items := []Auditorium{}
		for rows.Next() {
			var v Auditorium
			if err := rows.Scan(&v.ID, &v.CinemaID, &v.Name, &v.Format, &v.Active); err != nil {
				serverError(w)
				return
			}
			items = append(items, v)
		}
		utils.WriteJSON(w, http.StatusOK, items)
		return
	}
	var v Auditorium
	if !decode(w, r, &v) {
		return
	}
	if v.CinemaID == uuid.Nil || strings.TrimSpace(v.Name) == "" || strings.TrimSpace(v.Format) == "" {
		utils.WriteError(w, http.StatusBadRequest, "invalid auditorium")
		return
	}
	v.ID = uuid.New()
	v.Active = true
	_, err := h.db.Exec(r.Context(), `INSERT INTO screens(id,cinema_id,name,format,active) VALUES($1,$2,$3,$4,true)`, v.ID, v.CinemaID, strings.TrimSpace(v.Name), strings.TrimSpace(v.Format))
	if err != nil {
		conflictOrServer(w, err)
		return
	}
	h.audit(r, "auditorium.created", "auditorium", v.ID.String(), v)
	utils.WriteJSON(w, http.StatusCreated, v)
}

func (h *CatalogHandler) Auditorium(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "auditoriumID")
	if !ok {
		return
	}
	if r.Method == http.MethodDelete {
		_, err := h.db.Exec(r.Context(), `UPDATE screens SET active=false WHERE id=$1`, id)
		if err != nil {
			serverError(w)
			return
		}
		h.audit(r, "auditorium.archived", "auditorium", id.String(), nil)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var v Auditorium
	if !decode(w, r, &v) {
		return
	}
	if v.CinemaID == uuid.Nil || strings.TrimSpace(v.Name) == "" || strings.TrimSpace(v.Format) == "" {
		utils.WriteError(w, http.StatusBadRequest, "invalid auditorium")
		return
	}
	_, err := h.db.Exec(r.Context(), `UPDATE screens SET cinema_id=$2,name=$3,format=$4,active=$5 WHERE id=$1`, id, v.CinemaID, strings.TrimSpace(v.Name), strings.TrimSpace(v.Format), v.Active)
	if err != nil {
		conflictOrServer(w, err)
		return
	}
	v.ID = id
	h.audit(r, "auditorium.updated", "auditorium", id.String(), v)
	utils.WriteJSON(w, http.StatusOK, v)
}

func (h *CatalogHandler) Layout(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "auditoriumID")
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		rows, err := h.db.Query(r.Context(), `SELECT id,row_label,seat_number,category,accessible FROM seats WHERE screen_id=$1 ORDER BY row_label,seat_number`, id)
		if err != nil {
			serverError(w)
			return
		}
		defer rows.Close()
		items := []SeatInput{}
		for rows.Next() {
			var v SeatInput
			if err := rows.Scan(&v.ID, &v.Row, &v.Number, &v.Category, &v.Accessible); err != nil {
				serverError(w)
				return
			}
			items = append(items, v)
		}
		utils.WriteJSON(w, http.StatusOK, items)
		return
	}
	var seats []SeatInput
	if !decode(w, r, &seats) {
		return
	}
	if len(seats) == 0 || len(seats) > 1000 {
		utils.WriteError(w, http.StatusBadRequest, "layout must contain 1-1000 seats")
		return
	}
	seen := map[string]bool{}
	for i := range seats {
		seats[i].ID = strings.ToUpper(strings.TrimSpace(seats[i].ID))
		seats[i].Row = strings.ToUpper(strings.TrimSpace(seats[i].Row))
		seats[i].Category = strings.ToLower(strings.TrimSpace(seats[i].Category))
		if seats[i].ID == "" || seats[i].Row == "" || seats[i].Number < 1 || seats[i].Category == "" || seen[seats[i].ID] {
			utils.WriteError(w, http.StatusBadRequest, "invalid or duplicate seat")
			return
		}
		seen[seats[i].ID] = true
	}
	tx, err := h.db.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		serverError(w)
		return
	}
	defer tx.Rollback(r.Context())
	var inUse bool
	if err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM screenings WHERE screen_id=$1 AND status='scheduled' AND starts_at>now())`, id).Scan(&inUse); err != nil {
		serverError(w)
		return
	}
	if inUse {
		utils.WriteError(w, http.StatusConflict, "layout cannot change while future shows are scheduled")
		return
	}
	if _, err = tx.Exec(r.Context(), `DELETE FROM seats WHERE screen_id=$1`, id); err != nil {
		conflictOrServer(w, err)
		return
	}
	for _, seat := range seats {
		if _, err = tx.Exec(r.Context(), `INSERT INTO seats(screen_id,id,row_label,seat_number,category,accessible) VALUES($1,$2,$3,$4,$5,$6)`, id, seat.ID, seat.Row, seat.Number, seat.Category, seat.Accessible); err != nil {
			conflictOrServer(w, err)
			return
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w)
		return
	}
	h.audit(r, "layout.replaced", "auditorium", id.String(), map[string]int{"seat_count": len(seats)})
	utils.WriteJSON(w, http.StatusOK, seats)
}

func (h *CatalogHandler) PricingTiers(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		rows, err := h.db.Query(r.Context(), `SELECT id,cinema_id,name,seat_category,active FROM pricing_tiers ORDER BY cinema_id,name`)
		if err != nil {
			serverError(w)
			return
		}
		defer rows.Close()
		items := []PricingTier{}
		for rows.Next() {
			var v PricingTier
			if err := rows.Scan(&v.ID, &v.CinemaID, &v.Name, &v.SeatCategory, &v.Active); err != nil {
				serverError(w)
				return
			}
			items = append(items, v)
		}
		utils.WriteJSON(w, http.StatusOK, items)
		return
	}
	var v PricingTier
	if !decode(w, r, &v) {
		return
	}
	if v.CinemaID == uuid.Nil || strings.TrimSpace(v.Name) == "" || strings.TrimSpace(v.SeatCategory) == "" {
		utils.WriteError(w, http.StatusBadRequest, "invalid pricing tier")
		return
	}
	v.ID = uuid.New()
	v.Active = true
	_, err := h.db.Exec(r.Context(), `INSERT INTO pricing_tiers(id,cinema_id,name,seat_category,active) VALUES($1,$2,$3,$4,true)`, v.ID, v.CinemaID, strings.TrimSpace(v.Name), strings.ToLower(strings.TrimSpace(v.SeatCategory)))
	if err != nil {
		conflictOrServer(w, err)
		return
	}
	h.audit(r, "pricing_tier.created", "pricing_tier", v.ID.String(), v)
	utils.WriteJSON(w, http.StatusCreated, v)
}

func (h *CatalogHandler) PricingTier(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "tierID")
	if !ok {
		return
	}
	if r.Method == http.MethodDelete {
		_, err := h.db.Exec(r.Context(), `UPDATE pricing_tiers SET active=false,updated_at=now() WHERE id=$1`, id)
		if err != nil {
			serverError(w)
			return
		}
		h.audit(r, "pricing_tier.archived", "pricing_tier", id.String(), nil)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var v PricingTier
	if !decode(w, r, &v) {
		return
	}
	if v.CinemaID == uuid.Nil || strings.TrimSpace(v.Name) == "" || strings.TrimSpace(v.SeatCategory) == "" {
		utils.WriteError(w, http.StatusBadRequest, "invalid pricing tier")
		return
	}
	_, err := h.db.Exec(r.Context(), `UPDATE pricing_tiers SET cinema_id=$2,name=$3,seat_category=$4,active=$5,updated_at=now() WHERE id=$1`, id, v.CinemaID, strings.TrimSpace(v.Name), strings.ToLower(strings.TrimSpace(v.SeatCategory)), v.Active)
	if err != nil {
		conflictOrServer(w, err)
		return
	}
	v.ID = id
	h.audit(r, "pricing_tier.updated", "pricing_tier", id.String(), v)
	utils.WriteJSON(w, http.StatusOK, v)
}

func (h *CatalogHandler) Shows(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		rows, err := h.db.Query(r.Context(), `SELECT sc.id,sc.movie_id,sc.screen_id,sc.starts_at,sc.sales_end_at,
			sc.base_price_paise,sc.currency,sc.status,COALESCE((SELECT json_agg(json_build_object(
			'pricing_tier_id',sp.pricing_tier_id,'price_paise',sp.price_paise) ORDER BY sp.pricing_tier_id)
			FROM screening_prices sp WHERE sp.screening_id=sc.id),'[]'::json) prices
			FROM screenings sc ORDER BY sc.starts_at DESC LIMIT 500`)
		if err != nil {
			serverError(w)
			return
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var id, movieID, screenID uuid.UUID
			var starts, sales time.Time
			var price int64
			var currency, status string
			var prices json.RawMessage
			if err := rows.Scan(&id, &movieID, &screenID, &starts, &sales, &price, &currency, &status, &prices); err != nil {
				serverError(w)
				return
			}
			items = append(items, map[string]any{"id": id, "movie_id": movieID, "auditorium_id": screenID, "starts_at": starts, "sales_end_at": sales, "base_price_paise": price, "currency": currency, "status": status, "prices": prices})
		}
		utils.WriteJSON(w, http.StatusOK, items)
		return
	}
	var v ShowInput
	if !decode(w, r, &v) {
		return
	}
	if !validShow(v) {
		utils.WriteError(w, http.StatusBadRequest, "invalid show")
		return
	}
	id := uuid.New()
	if err := h.saveShow(r.Context(), id, v, true); err != nil {
		conflictOrServer(w, err)
		return
	}
	h.audit(r, "show.created", "show", id.String(), v)
	utils.WriteJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (h *CatalogHandler) Show(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "showID")
	if !ok {
		return
	}
	if r.Method == http.MethodDelete {
		tag, err := h.db.Exec(r.Context(), `UPDATE screenings SET status='cancelled' WHERE id=$1 AND NOT EXISTS(SELECT 1 FROM orders WHERE screening_id=$1 AND status IN ('payment_pending','confirmed','refund_pending'))`, id)
		if err != nil {
			serverError(w)
			return
		}
		if tag.RowsAffected() != 1 {
			utils.WriteError(w, http.StatusConflict, "show does not exist or has active bookings")
			return
		}
		h.audit(r, "show.cancelled", "show", id.String(), nil)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var v ShowInput
	if !decode(w, r, &v) {
		return
	}
	if !validShow(v) {
		utils.WriteError(w, http.StatusBadRequest, "invalid show")
		return
	}
	if err := h.saveShow(r.Context(), id, v, false); err != nil {
		conflictOrServer(w, err)
		return
	}
	h.audit(r, "show.updated", "show", id.String(), v)
	utils.WriteJSON(w, http.StatusOK, map[string]any{"id": id})
}

func validShow(v ShowInput) bool {
	return v.MovieID != uuid.Nil && v.AuditoriumID != uuid.Nil && !v.StartsAt.IsZero() && !v.SalesEndAt.IsZero() && v.SalesEndAt.Before(v.StartsAt) && v.BasePricePaise > 0 && len(v.Currency) == 3 && (v.Status == "scheduled" || v.Status == "cancelled" || v.Status == "completed")
}

func (h *CatalogHandler) saveShow(ctx context.Context, id uuid.UUID, v ShowInput, create bool) error {
	tx, err := h.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var duration int
	if err = tx.QueryRow(ctx, `SELECT duration_minutes FROM movies WHERE id=$1 AND active`, v.MovieID).Scan(&duration); err != nil {
		return err
	}
	var overlap bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM screenings sc JOIN movies m ON m.id=sc.movie_id WHERE sc.screen_id=$1 AND sc.id<>$2 AND sc.status='scheduled' AND tstzrange(sc.starts_at,sc.starts_at+(m.duration_minutes * interval '1 minute'),'[)') && tstzrange($3::timestamptz,$3::timestamptz+($4::integer * interval '1 minute'),'[)'))`, v.AuditoriumID, id, v.StartsAt, duration).Scan(&overlap); err != nil {
		return err
	}
	if overlap {
		return errors.New("show overlaps another auditorium booking")
	}
	var tag pgconn.CommandTag
	if create {
		tag, err = tx.Exec(ctx, `INSERT INTO screenings(id,movie_id,screen_id,starts_at,sales_end_at,base_price_paise,currency,status) SELECT $1,$2,s.id,$4,$5,$6,$7,$8 FROM screens s WHERE s.id=$3 AND s.active`, id, v.MovieID, v.AuditoriumID, v.StartsAt, v.SalesEndAt, v.BasePricePaise, strings.ToUpper(v.Currency), v.Status)
	} else {
		tag, err = tx.Exec(ctx, `UPDATE screenings SET movie_id=$2,screen_id=$3,starts_at=$4,sales_end_at=$5,base_price_paise=$6,currency=$7,status=$8 WHERE id=$1 AND EXISTS(SELECT 1 FROM screens WHERE id=$3 AND active) AND NOT EXISTS(SELECT 1 FROM orders WHERE screening_id=$1)`, id, v.MovieID, v.AuditoriumID, v.StartsAt, v.SalesEndAt, v.BasePricePaise, strings.ToUpper(v.Currency), v.Status)
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("show does not exist, auditorium is inactive, or bookings prevent changes")
	}
	if _, err = tx.Exec(ctx, `DELETE FROM screening_prices WHERE screening_id=$1`, id); err != nil {
		return err
	}
	for _, price := range v.Prices {
		if price.PricingTierID == uuid.Nil || price.PricePaise <= 0 {
			return errors.New("invalid show price")
		}
		tag, e := tx.Exec(ctx, `INSERT INTO screening_prices(screening_id,pricing_tier_id,price_paise) SELECT $1,pt.id,$3 FROM pricing_tiers pt JOIN screens s ON s.id=$4 WHERE pt.id=$2 AND pt.cinema_id=s.cinema_id AND pt.active`, id, price.PricingTierID, price.PricePaise, v.AuditoriumID)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return errors.New("pricing tier does not belong to auditorium cinema")
		}
	}
	return tx.Commit(ctx)
}

func (h *CatalogHandler) audit(r *http.Request, action, entityType, entityID string, details any) {
	user, _ := auth.UserFromContext(r.Context())
	payload, _ := json.Marshal(details)
	_, _ = h.db.Exec(r.Context(), `INSERT INTO admin_audit_log(actor_user_id,action,entity_type,entity_id,details) VALUES($1,$2,$3,$4,$5)`, user.ID, action, entityType, entityID, payload)
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		utils.WriteError(w, http.StatusBadRequest, "request body must contain one JSON value")
		return false
	}
	return true
}
func pathID(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid "+name)
		return uuid.Nil, false
	}
	return id, true
}
func serverError(w http.ResponseWriter) {
	utils.WriteError(w, http.StatusInternalServerError, "internal server error")
}
func conflictOrServer(w http.ResponseWriter, err error) {
	if errors.Is(err, pgx.ErrNoRows) || strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "overlaps") || strings.Contains(err.Error(), "foreign key") || strings.Contains(err.Error(), "pricing tier") {
		utils.WriteError(w, http.StatusConflict, err.Error())
		return
	}
	log.Printf("admin catalog operation failed: %v", err)
	serverError(w)
}
