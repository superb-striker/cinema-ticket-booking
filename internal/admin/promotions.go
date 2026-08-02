package admin

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/superb-striker/cinema/internal/utils"
)

type Promotion struct {
	ID                   uuid.UUID  `json:"id"`
	CinemaID             *uuid.UUID `json:"cinema_id,omitempty"`
	Code                 string     `json:"code"`
	Name                 string     `json:"name"`
	DiscountType         string     `json:"discount_type"`
	DiscountValue        int64      `json:"discount_value"`
	MinimumSubtotalPaise int64      `json:"minimum_subtotal_paise"`
	MaximumDiscountPaise *int64     `json:"maximum_discount_paise,omitempty"`
	StartsAt             time.Time  `json:"starts_at"`
	EndsAt               time.Time  `json:"ends_at"`
	MaximumRedemptions   *int       `json:"maximum_redemptions,omitempty"`
	PerCustomerLimit     int        `json:"per_customer_limit"`
	Active               bool       `json:"active"`
	RedemptionCount      int        `json:"redemption_count"`
}

func (h *CatalogHandler) Promotions(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		rows, err := h.db.Query(r.Context(), `SELECT p.id,p.cinema_id,p.code,p.name,p.discount_type,
			p.discount_value,p.minimum_subtotal_paise,p.maximum_discount_paise,p.starts_at,p.ends_at,
			p.maximum_redemptions,p.per_customer_limit,p.active,count(cr.order_id)
			FROM promotions p LEFT JOIN coupon_redemptions cr ON cr.promotion_id=p.id
			GROUP BY p.id ORDER BY p.created_at DESC`)
		if err != nil {
			serverError(w)
			return
		}
		defer rows.Close()
		items := []Promotion{}
		for rows.Next() {
			var v Promotion
			if err := rows.Scan(&v.ID, &v.CinemaID, &v.Code, &v.Name, &v.DiscountType,
				&v.DiscountValue, &v.MinimumSubtotalPaise, &v.MaximumDiscountPaise,
				&v.StartsAt, &v.EndsAt, &v.MaximumRedemptions, &v.PerCustomerLimit,
				&v.Active, &v.RedemptionCount); err != nil {
				serverError(w)
				return
			}
			items = append(items, v)
		}
		utils.WriteJSON(w, http.StatusOK, items)
		return
	}
	var v Promotion
	if !decode(w, r, &v) {
		return
	}
	normalizePromotion(&v)
	if !validPromotion(v) {
		utils.WriteError(w, http.StatusBadRequest, "invalid promotion")
		return
	}
	v.ID, v.Active = uuid.New(), true
	_, err := h.db.Exec(r.Context(), `INSERT INTO promotions(id,cinema_id,code,name,discount_type,
		discount_value,minimum_subtotal_paise,maximum_discount_paise,starts_at,ends_at,
		maximum_redemptions,per_customer_limit,active)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,true)`, v.ID, v.CinemaID, v.Code,
		v.Name, v.DiscountType, v.DiscountValue, v.MinimumSubtotalPaise, v.MaximumDiscountPaise,
		v.StartsAt, v.EndsAt, v.MaximumRedemptions, v.PerCustomerLimit)
	if err != nil {
		conflictOrServer(w, err)
		return
	}
	h.audit(r, "promotion.created", "promotion", v.ID.String(), v)
	utils.WriteJSON(w, http.StatusCreated, v)
}

func (h *CatalogHandler) Promotion(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "promotionID")
	if !ok {
		return
	}
	if r.Method == http.MethodDelete {
		tag, err := h.db.Exec(r.Context(), `UPDATE promotions SET active=false,updated_at=now() WHERE id=$1`, id)
		if err != nil {
			serverError(w)
			return
		}
		if tag.RowsAffected() != 1 {
			utils.WriteError(w, http.StatusNotFound, "promotion not found")
			return
		}
		h.audit(r, "promotion.archived", "promotion", id.String(), nil)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var v Promotion
	if !decode(w, r, &v) {
		return
	}
	normalizePromotion(&v)
	if !validPromotion(v) {
		utils.WriteError(w, http.StatusBadRequest, "invalid promotion")
		return
	}
	tag, err := h.db.Exec(r.Context(), `UPDATE promotions SET cinema_id=$2,code=$3,name=$4,
		discount_type=$5,discount_value=$6,minimum_subtotal_paise=$7,maximum_discount_paise=$8,
		starts_at=$9,ends_at=$10,maximum_redemptions=$11,per_customer_limit=$12,active=$13,updated_at=now()
		WHERE id=$1`, id, v.CinemaID, v.Code, v.Name, v.DiscountType, v.DiscountValue,
		v.MinimumSubtotalPaise, v.MaximumDiscountPaise, v.StartsAt, v.EndsAt,
		v.MaximumRedemptions, v.PerCustomerLimit, v.Active)
	if err != nil {
		conflictOrServer(w, err)
		return
	}
	if tag.RowsAffected() != 1 {
		utils.WriteError(w, http.StatusNotFound, "promotion not found")
		return
	}
	v.ID = id
	h.audit(r, "promotion.updated", "promotion", id.String(), v)
	utils.WriteJSON(w, http.StatusOK, v)
}

func normalizePromotion(v *Promotion) {
	v.Code = strings.ToUpper(strings.TrimSpace(v.Code))
	v.Name = strings.TrimSpace(v.Name)
	v.DiscountType = strings.ToLower(strings.TrimSpace(v.DiscountType))
}

func validPromotion(v Promotion) bool {
	if v.Code == "" || len(v.Code) > 32 || v.Name == "" || len(v.Name) > 120 ||
		v.DiscountValue <= 0 || v.MinimumSubtotalPaise < 0 || v.PerCustomerLimit <= 0 ||
		v.StartsAt.IsZero() || !v.EndsAt.After(v.StartsAt) {
		return false
	}
	if v.DiscountType == "percentage" {
		return v.DiscountValue <= 100
	}
	return v.DiscountType == "fixed"
}
