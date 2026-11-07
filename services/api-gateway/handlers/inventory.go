package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/ayushdevani/forgecart/gen"
	"github.com/go-chi/chi/v5"
)

// UpdateStockRequest represents an inventory update payload
type UpdateStockRequest struct {
	Quantity int32 `json:"quantity" example:"50"`
}

// GetStock godoc
// @Summary      Get product stock
// @Description  Retrieves the current stock level for a product
// @Tags         inventory-service
// @Produce      json
// @Param        productId   path  string  true  "Product ID"
// @Success      200  {object}  map[string]interface{}
// @Failure      404  {string}  string "stock not found"
// @Router       /api/inventory/{productId} [get]
func GetStock(client gen.InventoryServiceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		productID := chi.URLParam(r, "productId")
		resp, err := client.GetStock(r.Context(), &gen.GetStockRequest{ProductId: productID})
		if err != nil {
			http.Error(w, "stock not found", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

// UpdateStock godoc
// @Summary      Update product stock
// @Description  Updates the stock level for a product (Admin only)
// @Tags         inventory-service
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        productId   path  string  true  "Product ID"
// @Param        request     body  UpdateStockRequest  true  "Stock update data"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {string}  string "invalid request"
// @Failure      401  {string}  string "unauthorized"
// @Failure      500  {string}  string "internal server error"
// @Router       /api/inventory/{productId} [put]
func UpdateStock(client gen.InventoryServiceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		productID := chi.URLParam(r, "productId")
		var req UpdateStockRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		resp, err := client.UpdateStock(r.Context(), &gen.UpdateStockRequest{
			ProductId: productID,
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
