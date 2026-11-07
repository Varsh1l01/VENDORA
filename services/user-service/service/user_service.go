package service

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/ayushdevani/forgecart/pkg/kafka"
	"github.com/ayushdevani/forgecart/services/user-service/repository"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUserExists         = errors.New("user already exists")
)

type UserService struct {
	repo      *repository.UserRepository
	jwtSecret string
	producer  *kafka.Producer
}

func NewUserService(repo *repository.UserRepository, jwtSecret string, producer *kafka.Producer) *UserService {
	return &UserService{
		repo:      repo,
		jwtSecret: jwtSecret,
		producer:  producer,
	}
}

func (s *UserService) Register(ctx context.Context, email, password, firstName, lastName string) (*repository.User, string, error) {
	// Check if user exists
	existing, _ := s.repo.GetByEmail(ctx, email)
	if existing != nil {
		return nil, "", ErrUserExists
	}

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, "", err
	}

	// Create user
	user := &repository.User{
		Email:     email,
		Password:  string(hashedPassword),
		FirstName: firstName,
		LastName:  lastName,
		Role:      "customer",
	}

	if err := s.repo.Create(ctx, user); err != nil {
		return nil, "", err
	}

	// Generate token
	token, err := s.generateToken(user)
	if err != nil {
		return nil, "", err
	}

	// Publish user.registered event
	if s.producer != nil {
		go func() {
			err := s.producer.Publish(context.Background(), "user.registered", map[string]interface{}{
				"user_id":    user.ID,
				"email":      user.Email,
				"first_name": user.FirstName,
			})
			if err != nil {
				log.Printf("Failed to publish user.registered event: %v", err)
			}
		}()
	}

	return user, token, nil
}

func (s *UserService) Login(ctx context.Context, email, password string) (*repository.User, string, error) {
	// Get user
	user, err := s.repo.GetByEmail(ctx, email)
	if err != nil {
		return nil, "", ErrInvalidCredentials
	}

	// Check password
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
		return nil, "", ErrInvalidCredentials
	}

	// Generate token
	token, err := s.generateToken(user)
	if err != nil {
		return nil, "", err
	}

	return user, token, nil
}

func (s *UserService) ValidateToken(tokenString string) (string, string, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		return []byte(s.jwtSecret), nil
	})

	if err != nil || !token.Valid {
		return "", "", errors.New("invalid token")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", "", errors.New("invalid claims")
	}

	userID, _ := claims["user_id"].(string)
	role, _ := claims["role"].(string)

	return userID, role, nil
}

func (s *UserService) GetUser(ctx context.Context, id string) (*repository.User, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *UserService) generateToken(user *repository.User) (string, error) {
	claims := jwt.MapClaims{
		"user_id": user.ID,
		"email":   user.Email,
		"role":    user.Role,
		"exp":     time.Now().Add(24 * time.Hour).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.jwtSecret))
}
