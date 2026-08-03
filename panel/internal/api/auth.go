package api

import (
	"crypto/rand"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/ladderairport/panel/internal/store"
	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookieName = "session"
	sessionTTL        = 24 * time.Hour

	adminPasswordLength   = 20
	adminPasswordAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

	loginMaxFailures = 5
	loginMaxBackoff  = 5 * time.Minute
)

// sessionClaims is the JWT payload for the admin session cookie.
// Version must match settings.session_version; logout and password changes
// bump it to revoke all issued tokens.
type sessionClaims struct {
	Version int64 `json:"ver"`
	jwt.RegisteredClaims
}

// loginAttempt tracks consecutive failures from one client IP.
type loginAttempt struct {
	failures    int
	lockedUntil time.Time
}

// EnsureAdminPassword generates a random initial admin password when the
// settings row has an empty AdminPasswordHash. The plaintext is logged once
// and never stored; only its bcrypt hash is persisted.
//
// When the LADDER_ADMIN_PASSWORD environment variable is set it becomes the
// initial password instead (for e2e tests and automated deployments); the
// value is bcrypt-hashed like a random one and never logged.
func EnsureAdminPassword(s *store.Store) error {
	st, err := s.GetSettings()
	if err != nil {
		return err
	}
	if st.AdminPasswordHash != "" {
		return nil
	}
	password := os.Getenv("LADDER_ADMIN_PASSWORD")
	if password == "" {
		password, err = randomInitialPassword(adminPasswordLength)
		if err != nil {
			return err
		}
		defer log.Printf("警告：管理员密码为空，已生成随机初始密码 %q（仅此一次显示），请立即登录并在系统设置中修改", password)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	st.AdminPasswordHash = string(hash)
	return s.SaveSettings(st)
}

// randomInitialPassword returns an n-character alphanumeric password.
func randomInitialPassword(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i, b := range buf {
		buf[i] = adminPasswordAlphabet[int(b)%len(adminPasswordAlphabet)]
	}
	return string(buf), nil
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
	version := int64(0)
	if st, err := s.Store.GetSettings(); err == nil {
		version = st.SessionVersion
	}
	now := time.Now()
	claims := sessionClaims{
		Version: version,
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
	claims, ok := token.Claims.(*sessionClaims)
	if !ok {
		return false
	}
	st, err := s.Store.GetSettings()
	if err != nil {
		return false
	}
	return claims.Version == st.SessionVersion
}

func (s *Server) authenticated(r *http.Request) bool {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return false
	}
	return s.validateSessionToken(c.Value)
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
	})
}

type loginRequest struct {
	Password string `json:"password"`
}

// clientIP returns the host part of the request's remote address.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || host == "" {
		return r.RemoteAddr
	}
	return host
}

// loginLockedFor returns the remaining lockout for ip (0 when not locked).
func (s *Server) loginLockedFor(ip string) time.Duration {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	attempt, ok := s.loginAttempts[ip]
	if !ok {
		return 0
	}
	remaining := time.Until(attempt.lockedUntil)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// recordLoginFailure applies exponential backoff (2^n seconds, capped at
// loginMaxBackoff) once failures reach loginMaxFailures.
func (s *Server) recordLoginFailure(ip string) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	if s.loginAttempts == nil {
		s.loginAttempts = map[string]*loginAttempt{}
	}
	attempt := s.loginAttempts[ip]
	if attempt == nil {
		attempt = &loginAttempt{}
		s.loginAttempts[ip] = attempt
	}
	attempt.failures++
	if attempt.failures >= loginMaxFailures {
		shift := min(attempt.failures-loginMaxFailures, 9)
		backoff := time.Second << shift
		if backoff > loginMaxBackoff {
			backoff = loginMaxBackoff
		}
		attempt.lockedUntil = time.Now().Add(backoff)
	}
}

func (s *Server) clearLoginFailures(ip string) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	delete(s.loginAttempts, ip)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "不允许使用该请求方法")
		return
	}
	var req loginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeDecodeError(w, err)
		return
	}
	if req.Password == "" {
		writeError(w, http.StatusBadRequest, "必须提供密码")
		return
	}
	ip := clientIP(r)
	if remaining := s.loginLockedFor(ip); remaining > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(remaining.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "登录失败次数过多，请稍后再试")
		return
	}
	st, err := s.Store.GetSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if st.AdminPasswordHash == "" || !CheckPassword(st.AdminPasswordHash, req.Password) {
		s.recordLoginFailure(ip)
		writeError(w, http.StatusUnauthorized, "密码错误")
		return
	}
	s.clearLoginFailures(ip)
	token, err := s.issueSessionToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "创建登录会话失败")
		return
	}
	s.setSessionCookie(w, r, token)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	// Revoke server-side when the caller still holds a valid session; the
	// endpoint stays public/idempotent so expired cookies can be cleared.
	if s.authenticated(r) {
		if _, err := s.Store.BumpSessionVersion(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}
