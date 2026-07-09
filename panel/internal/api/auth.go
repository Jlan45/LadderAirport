package api

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/ladderairport/panel/internal/store"
	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookieName = "session"
	sessionTTL        = 24 * time.Hour
	defaultAdminPass  = "admin"
)

// sessionClaims is the JWT payload for the admin session cookie.
type sessionClaims struct {
	jwt.RegisteredClaims
}

// EnsureAdminPassword sets a default bcrypt hash for "admin" when the
// settings row has an empty AdminPasswordHash. Logs a one-time warning.
func EnsureAdminPassword(s *store.Store) error {
	st, err := s.GetSettings()
	if err != nil {
		return err
	}
	if st.AdminPasswordHash != "" {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(defaultAdminPass), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	st.AdminPasswordHash = string(hash)
	if err := s.SaveSettings(st); err != nil {
		return err
	}
	log.Printf("WARNING: admin password was empty; default password %q has been set — change it in settings", defaultAdminPass)
	return nil
}

// HashPassword returns a bcrypt hash of password.
func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// CheckPassword compares plain password against a bcrypt hash.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func (s *Server) issueSessionToken() (string, error) {
	now := time.Now()
	claims := sessionClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "admin",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(sessionTTL)),
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString(s.Secret)
}

func (s *Server) validateSessionToken(tokenStr string) bool {
	if len(s.Secret) == 0 || tokenStr == "" {
		return false
	}
	token, err := jwt.ParseWithClaims(tokenStr, &sessionClaims{}, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, jwt.ErrTokenSignatureInvalid
		}
		return s.Secret, nil
	})
	if err != nil || !token.Valid {
		return false
	}
	return true
}

func (s *Server) authenticated(r *http.Request) bool {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return false
	}
	return s.validateSessionToken(c.Value)
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

type loginRequest struct {
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Password == "" {
		writeError(w, http.StatusBadRequest, "password required")
		return
	}
	st, err := s.Store.GetSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if st.AdminPasswordHash == "" || !CheckPassword(st.AdminPasswordHash, req.Password) {
		writeError(w, http.StatusUnauthorized, "invalid password")
		return
	}
	token, err := s.issueSessionToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue session")
		return
	}
	s.setSessionCookie(w, token)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
