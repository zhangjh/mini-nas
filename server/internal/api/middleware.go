package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type AuthMiddleware struct {
	JWTSecret []byte
}

func NewAuthMiddleware(secret string) *AuthMiddleware {
	return &AuthMiddleware{JWTSecret: []byte(secret)}
}

func (m *AuthMiddleware) GenerateToken(username string) (string, int64, error) {
	expiresAt := time.Now().Add(72 * time.Hour)
	claims := jwt.MapClaims{
		"exp":      expiresAt.Unix(),
		"iat":      time.Now().Unix(),
		"username": username,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	t, err := token.SignedString(m.JWTSecret)
	return t, expiresAt.Unix(), err
}

func (m *AuthMiddleware) VerifyToken(tokenStr string) bool {
	_, err := m.ParseToken(tokenStr)
	return err == nil
}

func (m *AuthMiddleware) ParseToken(tokenStr string) (string, error) {
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		return m.JWTSecret, nil
	})
	if err != nil || !token.Valid {
		return "", fmt.Errorf("invalid token")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", fmt.Errorf("invalid claims")
	}
	username, _ := claims["username"].(string)
	return username, nil
}

func (m *AuthMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("Authorization")
		if token == "" {
			token = r.URL.Query().Get("token")
		} else {
			token = strings.TrimPrefix(token, "Bearer ")
		}
		if token == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
				"ok": false, "error": "unauthorized",
			})
			return
		}
		username, err := m.ParseToken(token)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
				"ok": false, "error": "invalid token",
			})
			return
		}
		ctx := context.WithValue(r.Context(), usernameKey{}, username)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type usernameKey struct{}

func UsernameFromContext(ctx context.Context) string {
	v, _ := ctx.Value(usernameKey{}).(string)
	return v
}

func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}
