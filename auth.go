package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Auth signs users in with Supabase Auth (email + password) and verifies the
// access tokens it issues against the project's JWKS, so pages don't need a
// network call per request.
type Auth struct {
	url    string // https://<ref>.supabase.co
	apiKey string // publishable key
	client *http.Client

	mu        sync.Mutex
	keys      map[string]crypto.PublicKey // by kid
	fetchedAt time.Time
}

type Tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

type Claims struct {
	Sub   string `json:"sub"`
	Email string `json:"email"`
	Exp   int64  `json:"exp"`
	Iss   string `json:"iss"`
	Aud   any    `json:"aud"`
}

var (
	errBadLogin   = errors.New("อีเมลหรือรหัสผ่านไม่ถูกต้อง")
	errUnverified = errors.New("ยังไม่ได้ยืนยันอีเมล — ให้ผู้ดูแลกด Confirm ผู้ใช้ใน Supabase")
	errExpired    = errors.New("token expired")
)

func NewAuth(url, apiKey string) *Auth {
	return &Auth{url: strings.TrimRight(url, "/"), apiKey: apiKey, client: &http.Client{Timeout: 15 * time.Second}}
}

func (a *Auth) SignIn(ctx context.Context, email, password string) (Tokens, error) {
	return a.token(ctx, "password", map[string]string{"email": email, "password": password})
}

func (a *Auth) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	return a.token(ctx, "refresh_token", map[string]string{"refresh_token": refreshToken})
}

func (a *Auth) token(ctx context.Context, grant string, body map[string]string) (Tokens, error) {
	var t Tokens
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.url+"/auth/v1/token?grant_type="+grant, bytes.NewReader(b))
	if err != nil {
		return t, err
	}
	req.Header.Set("apikey", a.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return t, fmt.Errorf("ติดต่อ Supabase ไม่ได้: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			ErrorCode string `json:"error_code"`
			Msg       string `json:"msg"`
			Desc      string `json:"error_description"`
		}
		json.NewDecoder(resp.Body).Decode(&e)
		switch {
		case e.ErrorCode == "invalid_credentials" || e.Desc == "Invalid login credentials":
			return t, errBadLogin
		case e.ErrorCode == "email_not_confirmed":
			return t, errUnverified
		}
		return t, fmt.Errorf("supabase auth %d: %s%s", resp.StatusCode, e.Msg, e.Desc)
	}
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return t, err
	}
	if t.AccessToken == "" {
		return t, errors.New("supabase auth returned no access token")
	}
	return t, nil
}

// SignOut revokes the refresh token on Supabase (best effort).
func (a *Auth) SignOut(ctx context.Context, accessToken string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.url+"/auth/v1/logout", nil)
	if err != nil {
		return
	}
	req.Header.Set("apikey", a.apiKey)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	if resp, err := a.client.Do(req); err == nil {
		resp.Body.Close()
	}
}

// Verify checks an access token's signature, issuer, audience and expiry.
func (a *Auth) Verify(token string) (Claims, error) {
	var c Claims
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return c, errors.New("malformed token")
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := decodeSegment(parts[0], &hdr); err != nil {
		return c, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return c, err
	}
	key, err := a.key(hdr.Kid)
	if err != nil {
		return c, err
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	switch k := key.(type) {
	case *ecdsa.PublicKey:
		if hdr.Alg != "ES256" || len(sig) != 64 ||
			!ecdsa.Verify(k, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
			return c, errors.New("bad signature")
		}
	case *rsa.PublicKey:
		if hdr.Alg != "RS256" || rsa.VerifyPKCS1v15(k, crypto.SHA256, digest[:], sig) != nil {
			return c, errors.New("bad signature")
		}
	default:
		return c, errors.New("unsupported key")
	}
	if err := decodeSegment(parts[1], &c); err != nil {
		return c, err
	}
	if c.Iss != a.url+"/auth/v1" {
		return c, errors.New("wrong issuer")
	}
	if !audienceIs(c.Aud, "authenticated") {
		return c, errors.New("wrong audience")
	}
	if time.Now().Unix() >= c.Exp {
		return c, errExpired
	}
	return c, nil
}

func audienceIs(aud any, want string) bool {
	switch v := aud.(type) {
	case string:
		return v == want
	case []any:
		for _, x := range v {
			if x == want {
				return true
			}
		}
	}
	return false
}

func decodeSegment(seg string, v any) error {
	b, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// key returns the signing key for kid, refreshing the JWKS at most once a minute
// so a rotated key is picked up without hammering Supabase on bad tokens.
func (a *Auth) key(kid string) (crypto.PublicKey, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if k, ok := a.keys[kid]; ok {
		return k, nil
	}
	if time.Since(a.fetchedAt) < time.Minute {
		return nil, errors.New("unknown signing key")
	}
	a.fetchedAt = time.Now()
	keys, err := a.fetchJWKS()
	if err != nil {
		return nil, err
	}
	a.keys = keys
	if k, ok := keys[kid]; ok {
		return k, nil
	}
	return nil, errors.New("unknown signing key")
}

func (a *Auth) fetchJWKS() (map[string]crypto.PublicKey, error) {
	resp, err := a.client.Get(a.url + "/auth/v1/.well-known/jwks.json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var set struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			Crv string `json:"crv"`
			X   string `json:"x"`
			Y   string `json:"y"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return nil, fmt.Errorf("jwks: %w", err)
	}
	out := map[string]crypto.PublicKey{}
	for _, k := range set.Keys {
		switch {
		case k.Kty == "EC" && k.Crv == "P-256":
			x, err1 := base64.RawURLEncoding.DecodeString(k.X)
			y, err2 := base64.RawURLEncoding.DecodeString(k.Y)
			if err1 != nil || err2 != nil || len(x) != 32 || len(y) != 32 {
				continue
			}
			pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), append(append([]byte{4}, x...), y...))
			if err == nil {
				out[k.Kid] = pub
			}
		case k.Kty == "RSA":
			n, err1 := base64.RawURLEncoding.DecodeString(k.N)
			e, err2 := base64.RawURLEncoding.DecodeString(k.E)
			if err1 != nil || err2 != nil {
				continue
			}
			out[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		}
	}
	return out, nil
}
