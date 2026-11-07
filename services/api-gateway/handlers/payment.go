package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/ayushdevani/forgecart/gen"
	"github.com/ayushdevani/forgecart/pkg/middleware"
	"github.com/go-chi/chi/v5"
)

// CreatePaymentRequest represents a payment creation payload
type CreatePaymentRequest struct {
	OrderID string  `json:"order_id" example:"660e8400-e29b-41d4-a716-446655440001"`
	Amount  float64 `json:"amount" example:"99.99"`
}

// UpdatePaymentStatusRequest represents a payment status update payload
type UpdatePaymentStatusRequest struct {
	Status string `json:"status" example:"completed"`
}

// CreatePayment godoc
// @Summary      Create a payment
// @Description  Initiates a payment for the specified order
// @Tags         payment-service
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        request body CreatePaymentRequest true "Payment data"
// @Success      201  {object}  map[string]interface{}
// @Failure      400  {string}  string "invalid request"
// @Failure      401  {string}  string "unauthorized"
// @Failure      500  {string}  string "internal server error"
// @Router       /api/payments [post]
func CreatePayment(client gen.PaymentServiceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := middleware.GetUserID(r.Context())
		var req CreatePaymentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		resp, err := client.CreatePayment(r.Context(), &gen.CreatePaymentRequest{
			OrderId: req.OrderID,
			UserId:  userID,
			Amount:  req.Amount,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(resp)
	}
}

// GetPayment godoc
// @Summary      Get payment by order ID
// @Description  Retrieves payment details for a specific order
// @Tags         payment-service
// @Produce      json
// @Security     BearerAuth
// @Param        orderId   path  string  true  "Order ID"
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {string}  string "unauthorized"
// @Failure      404  {string}  string "payment not found"
// @Router       /api/payments/order/{orderId} [get]
func GetPayment(client gen.PaymentServiceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orderID := chi.URLParam(r, "orderId")
		resp, err := client.GetPayment(r.Context(), &gen.GetPaymentRequest{OrderId: orderID})
		if err != nil {
			http.Error(w, "payment not found", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

// UpdatePaymentStatus godoc
// @Summary      Update payment status
// @Description  Updates the status of a payment (internal use)
// @Tags         payment-service
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id       path  string  true  "Payment ID"
// @Param        request  body  UpdatePaymentStatusRequest  true  "Status update"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {string}  string "invalid request"
// @Failure      401  {string}  string "unauthorized"
// @Failure      500  {string}  string "internal server error"
// @Router       /api/payments/{id}/status [put]
func UpdatePaymentStatus(client gen.PaymentServiceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req UpdatePaymentStatusRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		resp, err := client.UpdatePaymentStatus(r.Context(), &gen.UpdatePaymentStatusRequest{
			Id:     id,
			Status: req.Status,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}
