package api

import (
	"encoding/json"
	"net/http"

	"github.com/zhangjh/mini-nas/server/internal/model"
)

type AuthHandler struct {
	auth     *AuthMiddleware
	password string
}

func NewAuthHandler(auth *AuthMiddleware, password string) *AuthHandler {
	return &AuthHandler{auth: auth, password: password}
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req model.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{
			OK: false, Error: "invalid request body",
		})
		return
	}

	if req.Password != h.password {
		writeJSON(w, http.StatusUnauthorized, model.APIResponse{
			OK: false, Error: "incorrect password",
		})
		return
	}

	token, expiresAt, err := h.auth.GenerateToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, model.APIResponse{
			OK: false, Error: "token generation failed",
		})
		return
	}

	writeJSON(w, http.StatusOK, model.APIResponse{
		OK: true,
		Data: model.LoginResponse{
			Token:     token,
			ExpiresAt: expiresAt,
		},
	})
}
