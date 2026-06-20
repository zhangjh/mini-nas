package api

import (
	"encoding/json"
	"net/http"

	"github.com/zhangjh/mini-nas/server/internal/model"
	"github.com/zhangjh/mini-nas/server/internal/service"
)

type AuthHandler struct {
	auth     *AuthMiddleware
	userSvc  *service.UserService
}

func NewAuthHandler(auth *AuthMiddleware, userSvc *service.UserService) *AuthHandler {
	return &AuthHandler{auth: auth, userSvc: userSvc}
}

func (h *AuthHandler) Status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, model.APIResponse{
		OK:   true,
		Data: model.AuthStatus{HasUsers: h.userSvc.HasUsers()},
	})
}

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req model.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{
			OK: false, Error: "invalid request body",
		})
		return
	}

	// If users already exist, registration requires authentication.
	if h.userSvc.HasUsers() {
		token := r.Header.Get("Authorization")
		if token == "" {
			token = r.URL.Query().Get("token")
		} else {
			token = trimBearer(token)
		}
		if token == "" || !h.auth.VerifyToken(token) {
			writeJSON(w, http.StatusForbidden, model.APIResponse{
				OK: false, Error: "registration requires authentication",
			})
			return
		}
	}

	user, err := h.userSvc.Register(req.Username, req.Password)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{
			OK: false, Error: err.Error(),
		})
		return
	}

	token, expiresAt, err := h.auth.GenerateToken(user.Username)
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
			Username:  user.Username,
		},
	})
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req model.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{
			OK: false, Error: "invalid request body",
		})
		return
	}

	user, err := h.userSvc.Login(req.Username, req.Password)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, model.APIResponse{
			OK: false, Error: "invalid username or password",
		})
		return
	}

	token, expiresAt, err := h.auth.GenerateToken(user.Username)
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
			Username:  user.Username,
		},
	})
}

func trimBearer(s string) string {
	if len(s) > 7 && s[:7] == "Bearer " {
		return s[7:]
	}
	return s
}
