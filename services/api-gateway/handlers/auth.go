package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/ayushdevani/forgecart/gen"
)

// RegisterRequest represents the registration payload
type RegisterRequest struct {
	Email     string `json:"email" example:"user@example.com"`
	Password  string `json:"password" example:"password123"`
	FirstName string `json:"first_name" example:"John"`
	LastName  string `json:"last_name" example:"Doe"`
}

// LoginRequest represents the login payload
type LoginRequest struct {
	Email    string `json:"email" example:"user@example.com"`
	Password string `json:"password" example:"password123"`
}

// Register godoc
// @Summary      Register a new user
// @Description  Creates a new user account in the system
// @Tags         user-service
// @Accept       json
// @Produce      json
// @Param        request body RegisterRequest true "User registration data"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {string}  string "invalid request"
// @Router       /api/auth/register [post]
func Register(client gen.UserServiceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req RegisterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		resp, err := client.Register(r.Context(), &gen.RegisterRequest{
			Email:     req.Email,
			Password:  req.Password,
			FirstName: req.FirstName,
			LastName:  req.LastName,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

// Login godoc
// @Summary      Authenticate a user
// @Description  Authenticates user credentials and returns a JWT token
// @Tags         user-service
// @Accept       json
// @Produce      json
// @Param        request body LoginRequest true "User login credentials"
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {string}  string "invalid credentials"
// @Router       /api/auth/login [post]
func Login(client gen.UserServiceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req LoginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		resp, err := client.Login(r.Context(), &gen.LoginRequest{
			Email:    req.Email,
			Password: req.Password,
		})
		if err != nil {
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}
