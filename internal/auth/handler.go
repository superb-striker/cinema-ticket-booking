package auth

import (
	"encoding/json"
	"errors"
	"io"
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
	var body credentials
	if !decodeBody(w, r, &body) {
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

func (h *Handler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Phone string `json:"phone"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	current, _ := UserFromContext(r.Context())
	user, err := h.service.UpdatePhone(r.Context(), current.ID, body.Phone)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	utils.WriteJSON(w, http.StatusOK, user)
}

func (h *Handler) RequestPasswordReset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := h.service.RequestPasswordReset(r.Context(), body.Email); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "could not request password reset")
		return
	}
	utils.WriteJSON(w, http.StatusAccepted, map[string]string{"message": "If that account exists, a reset link will be sent."})
}

func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := h.service.ResetPassword(r.Context(), body.Token, body.Password); err != nil {
		if errors.Is(err, ErrInvalidResetToken) {
			utils.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		if errors.Is(err, ErrInvalidCredentials) {
			utils.WriteError(w, http.StatusBadRequest, "password must contain 12-72 characters")
			return
		}
		utils.WriteError(w, http.StatusInternalServerError, "could not reset password")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) GoogleLogin(w http.ResponseWriter, r *http.Request) {
	url, state, err := h.service.GoogleLoginURL(r.Context())
	if err != nil {
		if errors.Is(err, ErrSocialUnavailable) {
			utils.WriteError(w, http.StatusNotFound, "Google login is not configured")
			return
		}
		utils.WriteError(w, http.StatusInternalServerError, "could not start Google login")
		return
	}
	h.service.SetOAuthStateCookie(w, state)
	http.Redirect(w, r, url, http.StatusFound)
}

func (h *Handler) GoogleCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if !h.service.ValidOAuthState(r, state) {
		http.Redirect(w, r, "/?oauth_error=1", http.StatusFound)
		return
	}
	h.service.ClearOAuthStateCookie(w)
	_, token, err := h.service.GoogleCallback(r.Context(), state, r.URL.Query().Get("code"))
	if err != nil {
		http.Redirect(w, r, "/?oauth_error=1", http.StatusFound)
		return
	}
	h.service.SetCookie(w, token)
	http.Redirect(w, r, "/", http.StatusFound)
}

func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
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
