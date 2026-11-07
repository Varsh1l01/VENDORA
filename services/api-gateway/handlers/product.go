package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/ayushdevani/forgecart/gen"
	"github.com/go-chi/chi/v5"
)

// CreateProductRequest represents a product creation payload
type CreateProductRequest struct {
	Name        string  `json:"name" example:"Wireless Mouse"`
	Description string  `json:"description" example:"Ergonomic wireless mouse with USB receiver"`
	Price       float64 `json:"price" example:"29.99"`
	ImageURL    string  `json:"image_url" example:"https://example.com/mouse.jpg"`
	Stock       int32   `json:"stock" example:"100"`
}

// CreateProduct godoc
// @Summary      Create a new product
// @Description  Creates a new product in the catalog (Admin only)
// @Tags         product-service
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        request body CreateProductRequest true "Product data"
// @Success      201  {object}  map[string]interface{}
// @Failure      400  {string}  string "invalid request"
// @Failure      401  {string}  string "unauthorized"
// @Router       /api/products [post]
func CreateProduct(client gen.ProductServiceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req CreateProductRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		resp, err := client.CreateProduct(r.Context(), &gen.CreateProductRequest{
			Name:        req.Name,
			Description: req.Description,
			Price:       req.Price,
			ImageUrl:    req.ImageURL,
			Stock:       req.Stock,
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

// ListProducts godoc
// @Summary      List all products
// @Description  Retrieves a paginated list of products from the catalog
// @Tags         product-service
// @Produce      json
// @Param        page   query  int  false  "Page number"  default(1)
// @Param        limit  query  int  false  "Items per page"  default(10)
// @Success      200  {object}  map[string]interface{}
// @Failure      500  {string}  string "internal server error"
// @Router       /api/products [get]
func ListProducts(client gen.ProductServiceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if page < 1 {
			page = 1
		}
		if limit < 1 {
			limit = 10
		}

		resp, err := client.ListProducts(r.Context(), &gen.ListProductsRequest{
			Page:  int32(page),
			Limit: int32(limit),
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

// GetProduct godoc
// @Summary      Get a product by ID
// @Description  Retrieves product details by its unique identifier
// @Tags         product-service
// @Produce      json
// @Param        id   path  string  true  "Product ID"
// @Success      200  {object}  map[string]interface{}
// @Failure      404  {string}  string "product not found"
// @Router       /api/products/{id} [get]
func GetProduct(client gen.ProductServiceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		resp, err := client.GetProduct(r.Context(), &gen.GetProductRequest{Id: id})
		if err != nil {
			http.Error(w, "product not found", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}
