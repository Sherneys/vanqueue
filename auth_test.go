package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeSupabase issues ES256 tokens like Supabase Auth and serves its JWKS.
type fakeSupabase struct {
	*httptest.Server
	key       *ecdsa.PrivateKey
	tokenTTL  time.Duration
	refreshes int
}

func newFakeSupabase(t *testing.T) *fakeSupabase {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSupabase{key: key, tokenTTL: time.Hour}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/v1/.well-known/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		pub, _ := key.PublicKey.Bytes() // 0x04 || X || Y
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kid": "k1", "kty": "EC", "crv": "P-256", "alg": "ES256",
			"x": base64.RawURLEncoding.EncodeToString(pub[1:33]),
			"y": base64.RawURLEncoding.EncodeToString(pub[33:]),
		}}})
	})
	mux.HandleFunc("POST /auth/v1/token", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Query().Get("grant_type") {
		case "password":
			if body["email"] != "staff@example.com" || body["password"] != "correct-horse" {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"code":400,"error_code":"invalid_credentials","msg":"Invalid login credentials"}`))
				return
			}
		case "refresh_token":
			if body["refresh_token"] != "refresh-1" {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"error_code":"refresh_token_not_found"}`))
				return
			}
			f.refreshes++
		}
		json.NewEncoder(w).Encode(Tokens{AccessToken: f.sign(t, "staff@example.com", f.tokenTTL), RefreshToken: "refresh-1"})
	})
	mux.HandleFunc("POST /auth/v1/logout", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func (f *fakeSupabase) sign(t *testing.T, email string, ttl time.Duration) string {
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	input := enc(map[string]string{"alg": "ES256", "kid": "k1", "typ": "JWT"}) + "." +
		enc(map[string]any{"sub": "u1", "email": email, "aud": "authenticated", "iss": f.URL + "/auth/v1", "exp": time.Now().Add(ttl).Unix()})
	digest := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, f.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestVerify(t *testing.T) {
	f := newFakeSupabase(t)
	a := NewAuth(f.URL, "pk")
	good := f.sign(t, "a@b.c", time.Hour)
	if c, err := a.Verify(good); err != nil || c.Email != "a@b.c" {
		t.Fatalf("valid token: %v %+v", err, c)
	}
	if _, err := a.Verify(f.sign(t, "a@b.c", -time.Minute)); err != errExpired {
		t.Fatalf("expired token: got %v", err)
	}
	parts := strings.Split(good, ".")
	forged := parts[0] + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"email":"x@y.z","aud":"authenticated","exp":9999999999}`)) + "." + parts[2]
	if _, err := a.Verify(forged); err == nil {
		t.Fatal("forged payload accepted")
	}
	if _, err := NewAuth(strings.Replace(f.URL, "127.0.0.1", "localhost", 1), "pk").Verify(good); err == nil {
		t.Fatal("token from another issuer accepted")
	}
}

func newAuthServer(t *testing.T) (*server, *fakeSupabase) {
	f := newFakeSupabase(t)
	s, err := newServer(nil, NewAuth(f.URL, "pk"))
	if err != nil {
		t.Fatal(err)
	}
	return s, f
}

func protected(s *server) http.Handler {
	return s.guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello " + currentUser(r)))
	}))
}

func TestGuardRedirectsToLogin(t *testing.T) {
	s, _ := newAuthServer(t)
	rec := httptest.NewRecorder()
	protected(s).ServeHTTP(rec, httptest.NewRequest("GET", "/vans?x=1", nil))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login?next="+url.QueryEscape("/vans?x=1") {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestLoginFlow(t *testing.T) {
	s, f := newAuthServer(t)

	bad := httptest.NewRecorder()
	s.ServeHTTP(bad, postForm("/login", "email=staff@example.com&password=wrong"))
	if bad.Code != http.StatusUnauthorized || !strings.Contains(bad.Body.String(), errBadLogin.Error()) {
		t.Fatalf("bad password: %d", bad.Code)
	}

	ok := httptest.NewRecorder()
	s.ServeHTTP(ok, postForm("/login", "email=staff@example.com&password=correct-horse&next=/queue"))
	if ok.Code != http.StatusSeeOther || ok.Header().Get("Location") != "/queue" {
		t.Fatalf("login: %d %q", ok.Code, ok.Header().Get("Location"))
	}
	cookies := ok.Result().Cookies()
	if len(cookies) != 2 || !cookies[0].HttpOnly {
		t.Fatalf("cookies: %+v", cookies)
	}

	req := httptest.NewRequest("GET", "/", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	protected(s).ServeHTTP(rec, req)
	if rec.Body.String() != "hello staff@example.com" {
		t.Fatalf("protected page: %d %q", rec.Code, rec.Body.String())
	}

	// An expired access token is renewed with the refresh token.
	req = httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: accessCookie, Value: f.sign(t, "staff@example.com", -time.Minute)})
	req.AddCookie(&http.Cookie{Name: refreshCookie, Value: "refresh-1"})
	rec = httptest.NewRecorder()
	protected(s).ServeHTTP(rec, req)
	if rec.Body.String() != "hello staff@example.com" || f.refreshes != 1 || len(rec.Result().Cookies()) != 2 {
		t.Fatalf("refresh: %q refreshes=%d", rec.Body.String(), f.refreshes)
	}

	// A revoked refresh token signs the user out.
	req = httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: refreshCookie, Value: "revoked"})
	rec = httptest.NewRecorder()
	protected(s).ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("revoked refresh: %d", rec.Code)
	}
}

func TestCrossSitePostBlocked(t *testing.T) {
	s, f := newAuthServer(t)
	req := postForm("/settings", "needed=1")
	req.Header.Set("Origin", "https://evil.example")
	req.AddCookie(&http.Cookie{Name: accessCookie, Value: f.sign(t, "staff@example.com", time.Hour)})
	rec := httptest.NewRecorder()
	protected(s).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{"/queue": "/queue", "//evil.com": "/", "https://evil.com": "/", "/\\evil.com": "/", "": "/"} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func postForm(path, body string) *http.Request {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}
