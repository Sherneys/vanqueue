package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	accessCookie  = "fleet_access"
	refreshCookie = "fleet_refresh"
	sessionMaxAge = 30 * 24 * time.Hour
)

type userKey struct{}

func currentUser(r *http.Request) string {
	u, _ := r.Context().Value(userKey{}).(string)
	return u
}

func isPublic(path string) bool {
	return path == "/login" || strings.HasPrefix(path, "/static/")
}

// guard requires a signed-in user for every page except the login page and static
// files. An expired access token is renewed with the refresh token transparently.
func (s *server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
			http.Error(w, "cross-site request blocked", http.StatusForbidden)
			return
		}
		if isPublic(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		email, ok := s.session(w, r)
		if !ok {
			to := "/login"
			if r.Method == http.MethodGet && r.URL.Path != "/" {
				to += "?next=" + url.QueryEscape(r.URL.RequestURI())
			}
			http.Redirect(w, r, to, http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, email)))
	})
}

// session returns the signed-in user's email, refreshing the tokens when needed.
func (s *server) session(w http.ResponseWriter, r *http.Request) (string, bool) {
	if c, err := r.Cookie(accessCookie); err == nil {
		claims, err := s.auth.Verify(c.Value)
		if err == nil {
			return claims.Email, true
		}
		if !errors.Is(err, errExpired) {
			log.Printf("auth: rejected access token: %v", err)
		}
	}
	rc, err := r.Cookie(refreshCookie)
	if err != nil || rc.Value == "" {
		return "", false
	}
	tokens, err := s.auth.Refresh(r.Context(), rc.Value)
	if err != nil {
		clearSession(w, r)
		return "", false
	}
	claims, err := s.auth.Verify(tokens.AccessToken)
	if err != nil {
		log.Printf("auth: refreshed token rejected: %v", err)
		clearSession(w, r)
		return "", false
	}
	setSession(w, r, tokens)
	return claims.Email, true
}

func setSession(w http.ResponseWriter, r *http.Request, t Tokens) {
	for name, v := range map[string]string{accessCookie: t.AccessToken, refreshCookie: t.RefreshToken} {
		http.SetCookie(w, &http.Cookie{Name: name, Value: v, Path: "/", MaxAge: int(sessionMaxAge.Seconds()),
			HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode})
	}
}

func clearSession(w http.ResponseWriter, r *http.Request) {
	for _, name := range []string{accessCookie, refreshCookie} {
		http.SetCookie(w, &http.Cookie{Name: name, Path: "/", MaxAge: -1, HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode})
	}
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// sameOrigin rejects form posts from other sites (CSRF). Requests without an
// Origin header (non-browser clients) are allowed; they carry no cookies by accident.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	// Behind a reverse proxy (e.g. a hosting platform) Host may be rewritten.
	return u.Host == r.Host || (u.Host != "" && u.Host == r.Header.Get("X-Forwarded-Host"))
}

// safeNext keeps post-login redirects on this site.
func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") || next == "/login" {
		return "/"
	}
	return next
}

type loginPage struct {
	Email string
	Next  string
	Err   string
}

func (s *server) loginPage(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.URL.Query().Get("next"))
	if _, ok := s.session(w, r); ok {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	s.renderLogin(w, http.StatusOK, loginPage{Next: next})
}

func (s *server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.FormValue("email"))
	next := safeNext(r.FormValue("next"))
	tokens, err := s.auth.SignIn(r.Context(), email, r.FormValue("password"))
	if err == nil {
		_, err = s.auth.Verify(tokens.AccessToken)
	}
	if err != nil {
		msg := err.Error()
		if !errors.Is(err, errBadLogin) && !errors.Is(err, errUnverified) {
			log.Printf("login %s: %v", email, err)
			msg = "เข้าสู่ระบบไม่สำเร็จ ลองใหม่อีกครั้ง"
		}
		s.renderLogin(w, http.StatusUnauthorized, loginPage{Email: email, Next: next, Err: msg})
		return
	}
	setSession(w, r, tokens)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(accessCookie); err == nil {
		s.auth.SignOut(r.Context(), c.Value)
	}
	clearSession(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *server) renderLogin(w http.ResponseWriter, status int, p loginPage) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := s.login.ExecuteTemplate(w, "login.html", p); err != nil {
		log.Printf("render login: %v", err)
	}
}
