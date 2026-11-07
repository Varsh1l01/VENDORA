package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/ayushdevani/forgecart/gen"
	"github.com/ayushdevani/forgecart/pkg/middleware"
	"github.com/go-chi/chi/v5"
)

// AddToCartRequest represents a cart item addition payload
type AddToCartRequest struct {
	ProductID string `json:"product_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	Quantity  int32  `json:"quantity" example:"2"`
}

// GetCart godoc
// @Summary      Get user's cart
// @Description  Retrieves the current user's shopping cart with all items
// @Tags         cart-service
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {string}  string "unauthorized"
// @Failure      500  {string}  string "internal server error"
// @Router       /api/cart [get]
func GetCart(client gen.CartServiceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := middleware.GetUserID(r.Context())
		resp, err := client.GetCart(r.Context(), &gen.GetCartRequest{UserId: userID})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

// AddToCart godoc
// @Summary      Add item to cart
// @Description  Adds a product to the user's shopping cart
// @Tags         cart-service
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        request body AddToCartRequest true "Cart item data"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {string}  string "invalid request"
// @Failure      401  {string}  string "unauthorized"
// @Failure      500  {string}  string "internal server error"
// @Router       /api/cart/items [post]
func AddToCart(client gen.CartServiceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := middleware.GetUserID(r.Context())
		var req AddToCartRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		resp, err := client.AddItem(r.Context(), &gen.AddItemRequest{
			UserId:    userID,
			ProductId: req.ProductID,
			Quantity:  req.Quantity,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

// RemoveFromCart godoc
// @Summary      Remove item from cart
// @Description  Removes a product from the user's shopping cart
// @Tags         cart-service
// @Produce      json
// @Security     BearerAuth
// @Param        productId   path  string  true  "Product ID to remove"
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {string}  string "unauthorized"
// @Failure      500  {string}  string "internal server error"
// @Router       /api/cart/items/{productId} [delete]
func RemoveFromCart(client gen.CartServiceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := middleware.GetUserID(r.Context())
		productID := chi.URLParam(r, "productId")

		resp, err := client.RemoveItem(r.Context(), &gen.RemoveItemRequest{
			UserId:    userID,
			ProductId: productID,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}
