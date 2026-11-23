// package auth owns Google's admin sign-in flow, signed sessions, and the browser boundary for protected routes.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const sessionCookieName = "citizen_admin_session"
const oauthStateCookieName = "citizen_oauth_state"
const sessionDuration = 7 * 24 * time.Hour

// Config contains deployment values for admin authentication.
type Config struct {
	ClientID     string
	ClientSecret string
	CallbackURL  string
	SessionKey   string
	CookieDomain string
	ClientOrigin string
	Environment  string
	AdminEmails  []string
}

// Service keeps authentication policy and Google OAuth configuration together.
type Service struct {
	config Config
	oauth  *oauth2.Config
	admins map[string]struct{}
}

type session struct {
	Email     string `json:"email"`
	ExpiresAt int64  `json:"expires_at"`
}

// New builds an authentication service whose only admitted users come from the configured allowlist.
func New(config Config) *Service {
	admins := make(map[string]struct{}, len(config.AdminEmails))
	for _, email := range config.AdminEmails {
		normalized := strings.ToLower(strings.TrimSpace(email))
		if normalized != "" {
			admins[normalized] = struct{}{}
		}
	}
	config.ClientOrigin = strings.TrimRight(config.ClientOrigin, "/")

	service := &Service{config: config, admins: admins}
	if config.ClientID != "" && config.ClientSecret != "" && config.CallbackURL != "" {
		service.oauth = &oauth2.Config{
			ClientID:     config.ClientID,
			ClientSecret: config.ClientSecret,
			Endpoint:     google.Endpoint,
			RedirectURL:  config.CallbackURL,
			Scopes:       []string{"openid", "email", "profile"},
		}
	}
	return service
}

// Login starts Google OAuth with a short-lived state value and PKCE verifier.
func (service *Service) Login(w http.ResponseWriter, r *http.Request) {
	if service.oauth == nil {
		writeError(w, http.StatusServiceUnavailable, "Google sign-in is not configured.")
		return
	}
	state, err := randomToken(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not start sign-in.")
		return
	}
	verifier := oauth2.GenerateVerifier()
	service.setCookie(w, oauthStateCookieName, state+"|"+verifier, 10*time.Minute, true)
	url := service.oauth.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))
	http.Redirect(w, r, url, http.StatusFound)
}

// Callback validates OAuth state, verifies the Google identity, and creates a session only for an allowlisted administrator.
func (service *Service) Callback(w http.ResponseWriter, r *http.Request) {
	if service.oauth == nil {
		writeError(w, http.StatusServiceUnavailable, "Google sign-in is not configured.")
		return
	}
	cookie, err := r.Cookie(oauthStateCookieName)
	service.clearCookie(w, oauthStateCookieName)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Sign-in expired. Please try again.")
		return
	}
	parts := strings.SplitN(cookie.Value, "|", 2)
	if len(parts) != 2 || !hmac.Equal([]byte(parts[0]), []byte(r.URL.Query().Get("state"))) {
		writeError(w, http.StatusBadRequest, "Sign-in expired. Please try again.")
		return
	}
	token, err := service.oauth.Exchange(r.Context(), r.URL.Query().Get("code"), oauth2.VerifierOption(parts[1]))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Google sign-in failed.")
		return
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "https://openidconnect.googleapis.com/v1/userinfo", nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not verify your Google account.")
		return
	}
	response, err := service.oauth.Client(r.Context(), token).Do(request)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Could not verify your Google account.")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		writeError(w, http.StatusUnauthorized, "Could not verify your Google account.")
		return
	}
	var identity struct {
		Email    string `json:"email"`
		Verified bool   `json:"email_verified"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&identity); err != nil {
		writeError(w, http.StatusUnauthorized, "Could not verify your Google account.")
		return
	}
	identity.Email = strings.ToLower(strings.TrimSpace(identity.Email))
	if !identity.Verified || identity.Email == "" {
		writeError(w, http.StatusForbidden, "Use a verified Google account on the admin allowlist.")
		return
	}
	if _, allowed := service.admins[identity.Email]; !allowed {
		writeError(w, http.StatusForbidden, "This Google account is not on the admin allowlist.")
		return
	}
	if len(service.config.SessionKey) < 32 {
		writeError(w, http.StatusServiceUnavailable, "Admin sessions are not configured.")
		return
	}
	encoded, err := service.signSession(session{Email: identity.Email, ExpiresAt: time.Now().Add(sessionDuration).Unix()})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not create an admin session.")
		return
	}
	service.setCookie(w, sessionCookieName, encoded, sessionDuration, true)
	http.Redirect(w, r, service.config.ClientOrigin+"/admin", http.StatusFound)
}

// Logout clears the admin session cookie.
func (service *Service) Logout(w http.ResponseWriter, _ *http.Request) {
	service.clearCookie(w, sessionCookieName)
	w.WriteHeader(http.StatusNoContent)
}

// Me returns the allowlisted email authenticated by the signed admin session.
func (service *Service) Me(w http.ResponseWriter, r *http.Request) {
	email := service.currentEmail(r)
	if email == "" {
		writeError(w, http.StatusUnauthorized, "Sign in with an allowlisted Google account to continue.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"email": email})
}

// RequireAdmin protects an API handler with a valid session and a current allowlist membership.
func (service *Service) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if service.currentEmail(r) == "" {
			writeError(w, http.StatusUnauthorized, "Sign in with an allowlisted Google account to continue.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// CORS allows credentialed browser calls only from the configured Citizen client origin.
func (service *Service) CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && origin == service.config.ClientOrigin {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			w.Header().Add("Vary", "Origin")
		} else if origin != "" {
			writeError(w, http.StatusForbidden, "This origin is not allowed.")
			return
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && origin != "" && origin != service.config.ClientOrigin {
			writeError(w, http.StatusForbidden, "This origin is not allowed.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// currentEmail validates the signed cookie, expiry, and current allowlist before returning the admin identity.
func (service *Service) currentEmail(r *http.Request) string {
	if len(service.config.SessionKey) < 32 {
		return ""
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 {
		return ""
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ""
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(signature, service.signature(data)) {
		return ""
	}
	var value session
	if json.Unmarshal(data, &value) != nil || value.ExpiresAt <= time.Now().Unix() {
		return ""
	}
	value.Email = strings.ToLower(strings.TrimSpace(value.Email))
	if _, allowed := service.admins[value.Email]; !allowed {
		return ""
	}
	return value.Email
}

// signSession encodes and signs the short-lived admin session payload.
func (service *Service) signSession(value session) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data) + "." + base64.RawURLEncoding.EncodeToString(service.signature(data)), nil
}

// signature computes the message authentication code for a session payload.
func (service *Service) signature(data []byte) []byte {
	mac := hmac.New(sha256.New, []byte(service.config.SessionKey))
	_, _ = mac.Write(data)
	return mac.Sum(nil)
}

// setCookie writes a host-scoped cookie with secure production defaults.
func (service *Service) setCookie(w http.ResponseWriter, name, value string, duration time.Duration, httpOnly bool) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: "/", Domain: service.config.CookieDomain,
		HttpOnly: httpOnly, Secure: strings.EqualFold(service.config.Environment, "production"),
		SameSite: http.SameSiteLaxMode, MaxAge: int(duration.Seconds()),
	})
}

// clearCookie expires one cookie using the same scope used when it was created.
func (service *Service) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: "/", Domain: service.config.CookieDomain,
		HttpOnly: true, Secure: strings.EqualFold(service.config.Environment, "production"),
		SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

// randomToken creates a URL-safe value from cryptographically secure random bytes.
func randomToken(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

// writeError sends a small JSON error response without exposing internal details.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// writeJSON sends a JSON value with the expected response content type.
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
