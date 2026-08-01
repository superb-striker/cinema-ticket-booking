package admin

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/superb-striker/cinema/internal/auth"
	"github.com/superb-striker/cinema/internal/utils"
)

type SeatBlock struct {
	ID          uuid.UUID  `json:"id"`
	ScreenID    uuid.UUID  `json:"auditorium_id"`
	SeatID      string     `json:"seat_id"`
	ScreeningID *uuid.UUID `json:"show_id,omitempty"`
	BlockType   string     `json:"block_type"`
	Reason      string     `json:"reason"`
	StartsAt    time.Time  `json:"starts_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	ReleasedAt  *time.Time `json:"released_at,omitempty"`
	CreatedBy   *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

func (h *CatalogHandler) SeatBlocks(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		h.listSeatBlocks(w, r)
		return
	}
	var v SeatBlock
	if !decode(w, r, &v) {
		return
	}
	v.SeatID = strings.ToUpper(strings.TrimSpace(v.SeatID))
	v.BlockType = strings.ToLower(strings.TrimSpace(v.BlockType))
	v.Reason = strings.TrimSpace(v.Reason)
	if v.ScreenID == uuid.Nil || v.SeatID == "" || v.Reason == "" ||
		(v.BlockType != "maintenance" && v.BlockType != "accessibility" && v.BlockType != "other") ||
		(v.ExpiresAt != nil && !v.ExpiresAt.After(time.Now())) {
		utils.WriteError(w, http.StatusBadRequest, "invalid seat block")
		return
	}
	user, _ := auth.UserFromContext(r.Context())
	v.ID = uuid.New()
	v.CreatedBy = &user.ID
	v.StartsAt = time.Now()
	v.CreatedAt = v.StartsAt
	var screening any
	if v.ScreeningID != nil {
		screening = *v.ScreeningID
	}
	_, _ = h.db.Exec(r.Context(), `UPDATE seat_blocks SET released_at=expires_at
		WHERE screen_id=$1 AND seat_id=$2 AND released_at IS NULL AND expires_at<=now()`, v.ScreenID, v.SeatID)
	tag, err := h.db.Exec(r.Context(), `INSERT INTO seat_blocks
		(id,screen_id,seat_id,screening_id,block_type,reason,expires_at,created_by)
		SELECT $1,$2,$3,$4,$5,$6,$7,$8
		WHERE ($4::uuid IS NULL OR EXISTS(
			SELECT 1 FROM screenings WHERE id=$4 AND screen_id=$2))
		AND NOT EXISTS(
			SELECT 1 FROM seat_blocks existing
			WHERE existing.screen_id=$2 AND existing.seat_id=$3
			  AND existing.released_at IS NULL
			  AND (existing.expires_at IS NULL OR existing.expires_at>now())
			  AND (existing.screening_id IS NULL OR $4::uuid IS NULL OR existing.screening_id=$4))
		AND NOT EXISTS(
			SELECT 1 FROM order_seats os JOIN screenings sc ON sc.id=os.screening_id
			WHERE (($4::uuid IS NULL AND sc.screen_id=$2) OR os.screening_id=$4)
			  AND os.seat_id=$3
			  AND os.status IN ('payment_pending','confirmed'))`,
		v.ID, v.ScreenID, v.SeatID, screening, v.BlockType, v.Reason, v.ExpiresAt, user.ID)
	if err != nil {
		conflictOrServer(w, err)
		return
	}
	if tag.RowsAffected() != 1 {
		utils.WriteError(w, http.StatusConflict, "seat does not belong to the show or is already reserved")
		return
	}
	h.audit(r, "seat_block.created", "seat_block", v.ID.String(), v)
	utils.WriteJSON(w, http.StatusCreated, v)
}

func (h *CatalogHandler) listSeatBlocks(w http.ResponseWriter, r *http.Request) {
	screenID, err := optionalUUID(r.URL.Query().Get("auditorium_id"))
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid auditorium_id")
		return
	}
	showID, err := optionalUUID(r.URL.Query().Get("show_id"))
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid show_id")
		return
	}
	rows, err := h.db.Query(r.Context(), `SELECT id,screen_id,seat_id,screening_id,block_type,reason,
		starts_at,expires_at,released_at,created_by,created_at
		FROM seat_blocks
		WHERE ($1::uuid IS NULL OR screen_id=$1)
		  AND ($2::uuid IS NULL OR screening_id=$2)
		ORDER BY created_at DESC LIMIT 500`, screenID, showID)
	if err != nil {
		serverError(w)
		return
	}
	defer rows.Close()
	items := []SeatBlock{}
	for rows.Next() {
		var v SeatBlock
		if err := rows.Scan(&v.ID, &v.ScreenID, &v.SeatID, &v.ScreeningID, &v.BlockType,
			&v.Reason, &v.StartsAt, &v.ExpiresAt, &v.ReleasedAt, &v.CreatedBy, &v.CreatedAt); err != nil {
			serverError(w)
			return
		}
		items = append(items, v)
	}
	utils.WriteJSON(w, http.StatusOK, items)
}

func (h *CatalogHandler) ReleaseSeatBlock(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "blockID")
	if !ok {
		return
	}
	tag, err := h.db.Exec(r.Context(), `UPDATE seat_blocks SET released_at=now()
		WHERE id=$1 AND released_at IS NULL`, id)
	if err != nil {
		serverError(w)
		return
	}
	if tag.RowsAffected() != 1 {
		utils.WriteError(w, http.StatusNotFound, "active seat block not found")
		return
	}
	h.audit(r, "seat_block.released", "seat_block", id.String(), nil)
	w.WriteHeader(http.StatusNoContent)
}

func optionalUUID(value string) (any, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	id, err := uuid.Parse(value)
	if err != nil {
		return nil, err
	}
	return id, nil
}
