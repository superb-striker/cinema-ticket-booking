package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/superb-striker/cinema/internal/utils"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"
)

const CookieName = "cinema_session"
const oauthStateCookie = "cinema_oauth_state"

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrEmailTaken         = errors.New("email is already registered")
	ErrInvalidResetToken  = errors.New("password reset link is invalid or expired")
	ErrSocialUnavailable  = errors.New("social login is not configured")
)

type User struct {
	ID    uuid.UUID `json:"id"`
	Email string    `json:"email"`
	Phone string    `json:"phone,omitempty"`
}

var e164Pattern = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)

type Service struct {
	db            *pgxpool.Pool
	sessionTTL    time.Duration
	cookieSecure  bool
	now           func() time.Time
	publicBaseURL string
	oauthConfig   *oauth2.Config
	oidcVerifier  *oidc.IDTokenVerifier
}

func NewService(db *pgxpool.Pool, sessionTTL time.Duration, cookieSecure bool, publicBaseURL string) *Service {
	return &Service{db: db, sessionTTL: sessionTTL, cookieSecure: cookieSecure, publicBaseURL: strings.TrimRight(publicBaseURL, "/"), now: time.Now}
}

func (s *Service) ConfigureGoogle(ctx context.Context, clientID, clientSecret, redirectURL string) error {
	if clientID == "" || clientSecret == "" || redirectURL == "" {
		return nil
	}
	provider, err := oidc.NewProvider(ctx, "https://accounts.google.com")
	if err != nil {
		return err
	}
	s.oauthConfig = &oauth2.Config{ClientID: clientID, ClientSecret: clientSecret, Endpoint: provider.Endpoint(),
		RedirectURL: redirectURL, Scopes: []string{oidc.ScopeOpenID, "email", "profile"}}
	s.oidcVerifier = provider.Verifier(&oidc.Config{ClientID: clientID})
	return nil
}

func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// prevent attackers from discovering whether an email address is registered through response timing.
// a request for an unknown email returns much faster than a request for a real email with the wrong password.
// by measuring many requests, an attacker could enumerate registered accounts.
// `dummyPasswordHash` ensures the “user not found” path still performs a bcrypt comparison
var dummyPasswordHash = func() []byte {
	hash, _ := bcrypt.GenerateFromPassword([]byte("invalid-login-password"), bcrypt.DefaultCost)
	return hash
}()

func (s *Service) Register(ctx context.Context, email, password string) (User, string, error) {
	email = normalizeEmail(email)
	address, emailErr := mail.ParseAddress(email)
	if emailErr != nil || address.Address != email || len(email) > 254 || len(password) < 12 || len(password) > 72 {
		return User{}, "", ErrInvalidCredentials
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, "", err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return User{}, "", err
	}
	defer tx.Rollback(ctx)

	var user User
	err = tx.QueryRow(ctx,
		`INSERT INTO users (email, password_hash) VALUES ($1, $2)
		 ON CONFLICT (lower(email)) DO NOTHING RETURNING id, email`, email, string(hash)).Scan(&user.ID, &user.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, "", ErrEmailTaken
	}
	if err != nil {
		return User{}, "", err
	}
	token, tokenHash, err := newToken()
	if err != nil {
		return User{}, "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO auth_sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		tokenHash[:], user.ID, s.now().Add(s.sessionTTL))
	if err != nil {
		return User{}, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, "", err
	}
	return user, token, nil
}

func (s *Service) Login(ctx context.Context, email, password string) (User, string, error) {
	var user User
	passwordHash := string(dummyPasswordHash)
	err := s.db.QueryRow(ctx, `SELECT id, email, password_hash FROM users WHERE email = $1`, normalizeEmail(email)).
		Scan(&user.ID, &user.Email, &passwordHash)
	passwordErr := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password))
	if err != nil || passwordErr != nil {
		return User{}, "", ErrInvalidCredentials
	}
	token, tokenHash, err := newToken()
	if err != nil {
		return User{}, "", err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO auth_sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		tokenHash[:], user.ID, s.now().Add(s.sessionTTL))
	return user, token, err
}

func (s *Service) RequestPasswordReset(ctx context.Context, email string) error {
	token, tokenHash, err := newToken()
	if err != nil {
		return err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var user User
	err = tx.QueryRow(ctx, `SELECT id,email FROM users WHERE email=$1`, normalizeEmail(email)).Scan(&user.ID, &user.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM password_reset_tokens WHERE user_id=$1 OR expires_at<=now()`, user.ID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO password_reset_tokens(token_hash,user_id,expires_at) VALUES($1,$2,$3)`,
		tokenHash[:], user.ID, s.now().Add(time.Hour)); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"reset_url": s.publicBaseURL + "/?reset_token=" + token})
	if _, err = tx.Exec(ctx, `INSERT INTO notification_jobs(user_id,channel,recipient,template,payload)
		VALUES($1,'email',$2,'password_reset',$3)`, user.ID, user.Email, payload); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) ResetPassword(ctx context.Context, token, password string) error {
	if len(password) < 12 || len(password) > 72 {
		return ErrInvalidCredentials
	}
	hash := sha256.Sum256([]byte(token))
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var userID uuid.UUID
	err = tx.QueryRow(ctx, `UPDATE password_reset_tokens SET used_at=now()
		WHERE token_hash=$1 AND used_at IS NULL AND expires_at>now() RETURNING user_id`, hash[:]).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidResetToken
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET password_hash=$2 WHERE id=$1`, userID, string(passwordHash)); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM auth_sessions WHERE user_id=$1`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) GoogleLoginURL(ctx context.Context) (string, string, error) {
	if s.oauthConfig == nil {
		return "", "", ErrSocialUnavailable
	}
	state, stateHash, err := newToken()
	if err != nil {
		return "", "", err
	}
	nonce, _, err := newToken()
	if err != nil {
		return "", "", err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO oauth_states(state_hash,nonce,expires_at) VALUES($1,$2,$3)`,
		stateHash[:], nonce, s.now().Add(10*time.Minute))
	if err != nil {
		return "", "", err
	}
	return s.oauthConfig.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.AccessTypeOnline), state, nil
}

func (s *Service) GoogleCallback(ctx context.Context, state, code string) (User, string, error) {
	if s.oauthConfig == nil || s.oidcVerifier == nil {
		return User{}, "", ErrSocialUnavailable
	}
	stateHash := sha256.Sum256([]byte(state))
	var nonce string
	err := s.db.QueryRow(ctx, `DELETE FROM oauth_states WHERE state_hash=$1 AND expires_at>now() RETURNING nonce`, stateHash[:]).Scan(&nonce)
	if err != nil {
		return User{}, "", ErrInvalidCredentials
	}
	token, err := s.oauthConfig.Exchange(ctx, code)
	if err != nil {
		return User{}, "", ErrInvalidCredentials
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return User{}, "", ErrInvalidCredentials
	}
	idToken, err := s.oidcVerifier.Verify(ctx, rawIDToken)
	if err != nil || idToken.Nonce != nonce {
		return User{}, "", ErrInvalidCredentials
	}
	var claims struct {
		Subject       string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := idToken.Claims(&claims); err != nil || claims.Subject == "" || !claims.EmailVerified {
		return User{}, "", ErrInvalidCredentials
	}
	return s.loginSocialIdentity(ctx, "google", claims.Subject, claims.Email)
}

func (s *Service) loginSocialIdentity(ctx context.Context, provider, subject, email string) (User, string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return User{}, "", err
	}
	defer tx.Rollback(ctx)
	var user User
	err = tx.QueryRow(ctx, `SELECT u.id,u.email FROM oauth_identities i JOIN users u ON u.id=i.user_id
		WHERE i.provider=$1 AND i.subject=$2`, provider, subject).Scan(&user.ID, &user.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		randomPassword, _, tokenErr := newToken()
		if tokenErr != nil {
			return User{}, "", tokenErr
		}
		passwordHash, hashErr := bcrypt.GenerateFromPassword([]byte(randomPassword), bcrypt.DefaultCost)
		if hashErr != nil {
			return User{}, "", hashErr
		}
		err = tx.QueryRow(ctx, `INSERT INTO users(email,password_hash) VALUES($1,$2)
			ON CONFLICT(lower(email)) DO UPDATE SET email=EXCLUDED.email RETURNING id,email`, normalizeEmail(email), string(passwordHash)).Scan(&user.ID, &user.Email)
		if err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO oauth_identities(provider,subject,user_id) VALUES($1,$2,$3)`, provider, subject, user.ID)
		}
	}
	if err != nil {
		return User{}, "", err
	}
	sessionToken, tokenHash, err := newToken()
	if err != nil {
		return User{}, "", err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO auth_sessions(token_hash,user_id,expires_at) VALUES($1,$2,$3)`,
		tokenHash[:], user.ID, s.now().Add(s.sessionTTL)); err != nil {
		return User{}, "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return User{}, "", err
	}
	return user, sessionToken, nil
}

func (s *Service) RunMaintenance(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for name, query := range map[string]string{
				"auth sessions": `DELETE FROM auth_sessions WHERE expires_at <= now()`,
				"reset tokens":  `DELETE FROM password_reset_tokens WHERE expires_at <= now() OR used_at IS NOT NULL`,
				"OAuth states":  `DELETE FROM oauth_states WHERE expires_at <= now()`,
			} {
				if _, err := s.db.Exec(ctx, query); err != nil {
					log.Printf("purge expired %s: %v", name, err)
				}
			}
		}
	}
}

func (s *Service) Authenticate(ctx context.Context, token string) (User, error) {
	hash := sha256.Sum256([]byte(token))
	var user User
	err := s.db.QueryRow(ctx,
		`SELECT u.id, u.email, COALESCE(u.phone,'') FROM auth_sessions a JOIN users u ON u.id = a.user_id
		 WHERE a.token_hash = $1 AND a.expires_at > now()`, hash[:]).Scan(&user.ID, &user.Email, &user.Phone)
	return user, err
}

func (s *Service) Logout(ctx context.Context, token string) error {
	hash := sha256.Sum256([]byte(token))
	_, err := s.db.Exec(ctx, `DELETE FROM auth_sessions WHERE token_hash = $1`, hash[:])
	return err
}

func (s *Service) UpdatePhone(ctx context.Context, userID uuid.UUID, phone string) (User, error) {
	phone = strings.TrimSpace(phone)
	if phone != "" && !e164Pattern.MatchString(phone) {
		return User{}, errors.New("phone must use E.164 format, for example +919876543210")
	}
	var user User
	err := s.db.QueryRow(ctx, `UPDATE users SET phone=NULLIF($2,'') WHERE id=$1
		RETURNING id,email,COALESCE(phone,'')`, userID, phone).Scan(&user.ID, &user.Email, &user.Phone)
	return user, err
}

func (s *Service) SetCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: token, Path: "/", HttpOnly: true,
		Secure: s.cookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: int(s.sessionTTL.Seconds())})
}

func (s *Service) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Path: "/", HttpOnly: true,
		Secure: s.cookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

func (s *Service) SetOAuthStateCookie(w http.ResponseWriter, state string) {
	http.SetCookie(w, &http.Cookie{Name: oauthStateCookie, Value: state, Path: "/api/v1/auth/google/callback",
		HttpOnly: true, Secure: s.cookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: 600})
}

func (s *Service) ValidOAuthState(r *http.Request, state string) bool {
	cookie, err := r.Cookie(oauthStateCookie)
	return err == nil && hmacEqual(cookie.Value, state)
}

func (s *Service) ClearOAuthStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: oauthStateCookie, Path: "/api/v1/auth/google/callback",
		HttpOnly: true, Secure: s.cookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

func hmacEqual(left, right string) bool {
	leftHash := sha256.Sum256([]byte(left))
	rightHash := sha256.Sum256([]byte(right))
	return left != "" && subtle.ConstantTimeCompare(leftHash[:], rightHash[:]) == 1
}

func newToken() (string, [32]byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", [32]byte{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	return token, sha256.Sum256([]byte(token)), nil
}

type contextKey struct{}

func UserFromContext(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(contextKey{}).(User)
	return u, ok
}

func (s *Service) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(CookieName)
		if err != nil {
			utils.WriteError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		user, err := s.Authenticate(r.Context(), cookie.Value)
		if err != nil {
			utils.WriteError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKey{}, user)))
	})
}

func TokenFromRequest(r *http.Request) string {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	return c.Value
}
