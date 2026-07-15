package booking

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/superb-striker/cinema/internal/utils"
)

type handler struct {
	svc *Service
}

func NewHandler(svc *Service) *handler {
	return &handler{svc}
}

type holdSeatRequest struct {
	UserID string `json:"user_id"`
}

// writeError maps domain errors to HTTP status codes. Unknown errors are
// logged and returned as an opaque 500 so internals don't leak.
func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrSeatAlreadyBooked):
		utils.WriteError(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrNotHeld):
		utils.WriteError(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrSessionNotFound):
		utils.WriteError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrForbidden):
		utils.WriteError(w, http.StatusForbidden, err.Error())
	default:
		log.Printf("internal error: %v", err)
		utils.WriteError(w, http.StatusInternalServerError, "internal server error")
	}
}

// decodeUserID reads and validates the request body, writing a 400 on failure.
// TODO: replace with the user ID from a verified auth token.
func decodeUserID(w http.ResponseWriter, r *http.Request) (string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var req holdSeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid request body")
		return "", false
	}
	if req.UserID == "" {
		utils.WriteError(w, http.StatusBadRequest, "user_id is required")
		return "", false
	}
	return req.UserID, true
}

func (h *handler) HoldSeat(w http.ResponseWriter, r *http.Request) {
	movieID := r.PathValue("movieID")
	seatID := r.PathValue("seatID")

	userID, ok := decodeUserID(w, r)
	if !ok {
		return
	}

	session, err := h.svc.Book(r.Context(), Booking{
		UserID:  userID,
		SeatID:  seatID,
		MovieID: movieID,
	})
	if err != nil {
		writeError(w, err)
		return
	}

	type holdResponse struct {
		SessionID string `json:"session_id"`
		MovieID   string `json:"movieID"`
		SeatID    string `json:"seat_id"`
		ExpiresAt string `json:"expires_at"`
	}

	utils.WriteJSON(w, http.StatusCreated, holdResponse{
		SeatID:    seatID,
		MovieID:   session.MovieID,
		SessionID: session.ID,
		ExpiresAt: session.ExpiresAt.Format(time.RFC3339),
	})
}

// seatInfo deliberately omits other users' IDs.
// Seats not listed are available.
type seatInfo struct {
	SeatID    string `json:"seat_id"`
	Status    string `json:"status"` // "held" | "confirmed"
	Booked    bool   `json:"booked"`
	Confirmed bool   `json:"confirmed"`
	Mine      bool   `json:"mine"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

func (h *handler) ListSeats(w http.ResponseWriter, r *http.Request) {
	movieID := r.PathValue("movieID")
	// TODO: derive from the auth token instead of a query param.
	caller := r.URL.Query().Get("user_id")

	bookings, err := h.svc.ListBookings(r.Context(), movieID)
	if err != nil {
		writeError(w, err)
		return
	}

	seats := make([]seatInfo, 0, len(bookings))
	for _, b := range bookings {
		info := seatInfo{
			SeatID:    b.SeatID,
			Status:    b.Status,
			Booked:    true,
			Confirmed: b.Status == StatusConfirmed,
			Mine:      caller != "" && b.UserID == caller,
		}
		if b.Status == StatusHeld && !b.ExpiresAt.IsZero() {
			info.ExpiresAt = b.ExpiresAt.Format(time.RFC3339)
		}
		seats = append(seats, info)
	}

	utils.WriteJSON(w, http.StatusOK, seats)
}

type sessionResponse struct {
	SessionID string `json:"session_id"`
	MovieID   string `json:"movie_id"`
	SeatID    string `json:"seat_id"`
	UserID    string `json:"user_id"`
	Status    string `json:"status"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

func (h *handler) ConfirmSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("sessionID")

	userID, ok := decodeUserID(w, r)
	if !ok {
		return
	}

	session, err := h.svc.ConfirmSeat(r.Context(), sessionID, userID)
	if err != nil {
		writeError(w, err)
		return
	}

	utils.WriteJSON(w, http.StatusOK, sessionResponse{
		SessionID: session.ID,
		MovieID:   session.MovieID,
		SeatID:    session.SeatID,
		UserID:    session.UserID,
		Status:    session.Status,
	})
}

func (h *handler) ReleaseSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("sessionID")

	userID, ok := decodeUserID(w, r)
	if !ok {
		return
	}

	if err := h.svc.ReleaseSeat(r.Context(), sessionID, userID); err != nil {
		writeError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
