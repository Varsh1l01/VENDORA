package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/ayushdevani/forgecart/gen"
	"github.com/ayushdevani/forgecart/pkg/middleware"
	"github.com/go-chi/chi/v5"
)

// CreateOrder godoc
// @Summary      Create a new order
// @Description  Creates a new order from the user's current cart
// @Tags         order-service
// @Produce      json
// @Security     BearerAuth
// @Success      201  {object}  map[string]interface{}
// @Failure      400  {string}  string "bad request"
// @Failure      401  {string}  string "unauthorized"
// @Router       /api/orders [post]
func CreateOrder(client gen.OrderServiceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := middleware.GetUserID(r.Context())
		resp, err := client.CreateOrder(r.Context(), &gen.CreateOrderRequest{UserId: userID})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(resp)
	}
}

// ListOrders godoc
// @Summary      List user's orders
// @Description  Retrieves a paginated list of the user's orders
// @Tags         order-service
// @Produce      json
// @Security     BearerAuth
// @Param        page   query  int  false  "Page number"  default(1)
// @Param        limit  query  int  false  "Items per page"  default(10)
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {string}  string "unauthorized"
// @Failure      500  {string}  string "internal server error"
// @Router       /api/orders [get]
func ListOrders(client gen.OrderServiceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := middleware.GetUserID(r.Context())
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if page < 1 {
			page = 1
		}
		if limit < 1 {
			limit = 10
		}

		resp, err := client.ListOrders(r.Context(), &gen.ListOrdersRequest{
			UserId: userID,
			Page:   int32(page),
			Limit:  int32(limit),
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

// GetOrder godoc
// @Summary      Get order by ID
// @Description  Retrieves order details by its unique identifier
// @Tags         order-service
// @Produce      json
// @Security     BearerAuth
// @Param        id   path  string  true  "Order ID"
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {string}  string "unauthorized"
// @Failure      404  {string}  string "order not found"
// @Router       /api/orders/{id} [get]
func GetOrder(client gen.OrderServiceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		resp, err := client.GetOrder(r.Context(), &gen.GetOrderRequest{Id: id})
		if err != nil {
			http.Error(w, "order not found", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}
