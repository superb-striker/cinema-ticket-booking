package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/superb-striker/cinema/internal/utils"
	"golang.org/x/crypto/bcrypt"
)

const CookieName = "cinema_session"

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrEmailTaken         = errors.New("email is already registered")
)

type User struct {
	ID    uuid.UUID `json:"id"`
	Email string    `json:"email"`
}

type Service struct {
	db           *pgxpool.Pool
	sessionTTL   time.Duration
	cookieSecure bool
	now          func() time.Time
}

func NewService(db *pgxpool.Pool, sessionTTL time.Duration, cookieSecure bool) *Service {
	return &Service{db: db, sessionTTL: sessionTTL, cookieSecure: cookieSecure, now: time.Now}
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

func (s *Service) RunMaintenance(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.db.Exec(ctx, `DELETE FROM auth_sessions WHERE expires_at <= now()`); err != nil {
				log.Printf("purge expired auth sessions: %v", err)
			}
		}
	}
}

func (s *Service) Authenticate(ctx context.Context, token string) (User, error) {
	hash := sha256.Sum256([]byte(token))
	var user User
	err := s.db.QueryRow(ctx,
		`SELECT u.id, u.email FROM auth_sessions a JOIN users u ON u.id = a.user_id
		 WHERE a.token_hash = $1 AND a.expires_at > now()`, hash[:]).Scan(&user.ID, &user.Email)
	return user, err
}

func (s *Service) Logout(ctx context.Context, token string) error {
	hash := sha256.Sum256([]byte(token))
	_, err := s.db.Exec(ctx, `DELETE FROM auth_sessions WHERE token_hash = $1`, hash[:])
	return err
}

func (s *Service) SetCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: token, Path: "/", HttpOnly: true,
		Secure: s.cookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: int(s.sessionTTL.Seconds())})
}

func (s *Service) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Path: "/", HttpOnly: true,
		Secure: s.cookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
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
