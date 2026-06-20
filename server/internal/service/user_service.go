package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/zhangjh/mini-nas/server/internal/model"
	"golang.org/x/crypto/bcrypt"
)

type UserService struct {
	mu       sync.RWMutex
	users    []model.User
	filePath string
}

func NewUserService(dbDir string) *UserService {
	os.MkdirAll(dbDir, 0755)
	s := &UserService{
		users:    []model.User{},
		filePath: filepath.Join(dbDir, "users.json"),
	}
	s.load()
	return s
}

func (s *UserService) HasUsers() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.users) > 0
}

func (s *UserService) Register(username, password string) (*model.User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, fmt.Errorf("username is required")
	}
	if len(password) < 4 {
		return nil, fmt.Errorf("password must be at least 4 characters")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, u := range s.users {
		if u.Username == username {
			return nil, fmt.Errorf("username already exists")
		}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	user := model.User{
		ID:           uuid.NewString(),
		Username:     username,
		PasswordHash: string(hash),
		CreatedAt:    time.Now().UnixMilli(),
	}
	s.users = append(s.users, user)
	s.save()
	return &user, nil
}

func (s *UserService) Login(username, password string) (*model.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, u := range s.users {
		if u.Username == username {
			if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
				return nil, fmt.Errorf("incorrect password")
			}
			return &u, nil
		}
	}
	return nil, fmt.Errorf("user not found")
}

func (s *UserService) load() {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return
	}
	json.Unmarshal(data, &s.users)
}

func (s *UserService) save() {
	data, _ := json.MarshalIndent(s.users, "", "  ")
	os.WriteFile(s.filePath, data, 0644)
}
