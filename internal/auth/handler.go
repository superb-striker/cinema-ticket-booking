package auth

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/superb-striker/cinema/internal/utils"
)

type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func decodeCredentials(w http.ResponseWriter, r *http.Request) (credentials, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body credentials
	if err := dec.Decode(&body); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid request body")
		return credentials{}, false
	}
	return body, true
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeCredentials(w, r)
	if !ok {
		return
	}
	user, token, err := h.service.Register(r.Context(), body.Email, body.Password)
	if err != nil {
		if errors.Is(err, ErrEmailTaken) {
			utils.WriteError(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, ErrInvalidCredentials) {
			utils.WriteError(w, http.StatusBadRequest, "valid email and a 12-72 character password are required")
			return
		}
		utils.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	h.service.SetCookie(w, token)
	utils.WriteJSON(w, http.StatusCreated, user)
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeCredentials(w, r)
	if !ok {
		return
	}
	user, token, err := h.service.Login(r.Context(), body.Email, body.Password)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			utils.WriteError(w, http.StatusUnauthorized, ErrInvalidCredentials.Error())
			return
		}
		utils.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	h.service.SetCookie(w, token)
	utils.WriteJSON(w, http.StatusOK, user)
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if token := TokenFromRequest(r); token != "" {
		_ = h.service.Logout(r.Context(), token)
	}
	h.service.ClearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFromContext(r.Context())
	utils.WriteJSON(w, http.StatusOK, user)
}
