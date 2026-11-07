package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	cartv1 "github.com/Varsh1l01/VENDORA/gen/vendora/cart/v1"
	inventoryv1 "github.com/Varsh1l01/VENDORA/gen/vendora/inventory/v1"
	orderv1 "github.com/Varsh1l01/VENDORA/gen/vendora/order/v1"
	paymentv1 "github.com/Varsh1l01/VENDORA/gen/vendora/payment/v1"
	productv1 "github.com/Varsh1l01/VENDORA/gen/vendora/product/v1"
	userv1 "github.com/Varsh1l01/VENDORA/gen/vendora/user/v1"
	"github.com/Varsh1l01/VENDORA/pkg/auth"
	"github.com/Varsh1l01/VENDORA/pkg/validate"
)

// --- auth ------------------------------------------------------------------

type registerBody struct {
	Email     string `json:"email"`
	Password  string `json:"password"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var b registerBody
	if !decode(w, r, &b) {
		return
	}
	resp, err := s.c.User.Register(r.Context(), &userv1.RegisterRequest{
		Email: b.Email, Password: b.Password, FirstName: b.FirstName, LastName: b.LastName,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusCreated, resp)
}

type loginBody struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var b loginBody
	if !decode(w, r, &b) {
		return
	}
	resp, err := s.c.User.Login(r.Context(), &userv1.LoginRequest{Email: b.Email, Password: b.Password})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

type refreshBody struct {
	RefreshToken string `json:"refresh_token"`
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	var b refreshBody
	if !decode(w, r, &b) {
		return
	}
	resp, err := s.c.User.Refresh(r.Context(), &userv1.RefreshRequest{RefreshToken: b.RefreshToken})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	var b refreshBody
	if !decode(w, r, &b) {
		return
	}
	if _, err := s.c.User.Logout(r.Context(), &userv1.LogoutRequest{RefreshToken: b.RefreshToken}); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.FromContext(r.Context())
	resp, err := s.c.User.GetUser(call(r), &userv1.GetUserRequest{Id: p.Subject})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

// --- catalog ---------------------------------------------------------------

func pageParams(r *http.Request) (int32, string, bool) {
	q := r.URL.Query()
	size := int64(0)
	if v := q.Get("page_size"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil || n < 1 || n > validate.MaxPageSize {
			return 0, "", false
		}
		size = n
	}
	return int32(size), q.Get("page_token"), true
}

func (s *Server) listProducts(w http.ResponseWriter, r *http.Request) {
	size, tok, ok := pageParams(r)
	if !ok {
		writeProblem(w, r, http.StatusBadRequest, "page_size must be between 1 and 100")
		return
	}
	resp, err := s.c.Product.ListProducts(call(r), &productv1.ListProductsRequest{PageSize: size, PageToken: tok})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

func (s *Server) getProduct(w http.ResponseWriter, r *http.Request) {
	resp, err := s.c.Product.GetProduct(call(r), &productv1.GetProductRequest{Id: chi.URLParam(r, "id")})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

func (s *Server) getStock(w http.ResponseWriter, r *http.Request) {
	resp, err := s.c.Inventory.GetStock(call(r), &inventoryv1.GetStockRequest{ProductId: chi.URLParam(r, "id")})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

// --- cart ------------------------------------------------------------------

type cartItemBody struct {
	ProductID string `json:"product_id"`
	Quantity  int32  `json:"quantity"`
}

type quantityBody struct {
	Quantity int32 `json:"quantity"`
}

func (s *Server) getCart(w http.ResponseWriter, r *http.Request) {
	resp, err := s.c.Cart.GetCart(call(r), &cartv1.GetCartRequest{})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

func (s *Server) addCartItem(w http.ResponseWriter, r *http.Request) {
	var b cartItemBody
	if !decode(w, r, &b) {
		return
	}
	if err := validate.Quantity(b.Quantity); err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := s.c.Cart.AddItem(call(r), &cartv1.AddItemRequest{ProductId: b.ProductID, Quantity: b.Quantity})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

func (s *Server) setCartItem(w http.ResponseWriter, r *http.Request) {
	var b quantityBody
	if !decode(w, r, &b) {
		return
	}
	if err := validate.Quantity(b.Quantity); err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := s.c.Cart.SetItemQuantity(call(r), &cartv1.SetItemQuantityRequest{
		ProductId: chi.URLParam(r, "productId"), Quantity: b.Quantity,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

func (s *Server) removeCartItem(w http.ResponseWriter, r *http.Request) {
	resp, err := s.c.Cart.RemoveItem(call(r), &cartv1.RemoveItemRequest{ProductId: chi.URLParam(r, "productId")})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

func (s *Server) clearCart(w http.ResponseWriter, r *http.Request) {
	if _, err := s.c.Cart.ClearCart(call(r), &cartv1.ClearCartRequest{}); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- orders & payments -----------------------------------------------------

type createOrderBody struct {
	ShippingAddress string `json:"shipping_address"`
}

func (s *Server) createOrder(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 100 {
		writeProblem(w, r, http.StatusBadRequest, "Idempotency-Key header (1-100 chars) is required")
		return
	}
	var b createOrderBody
	if r.ContentLength != 0 && !decode(w, r, &b) {
		return
	}
	resp, err := s.c.Order.CreateOrder(call(r), &orderv1.CreateOrderRequest{
		IdempotencyKey: key, ShippingAddress: b.ShippingAddress,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusCreated, resp)
}

func (s *Server) listOrders(w http.ResponseWriter, r *http.Request) {
	size, tok, ok := pageParams(r)
	if !ok {
		writeProblem(w, r, http.StatusBadRequest, "page_size must be between 1 and 100")
		return
	}
	resp, err := s.c.Order.ListOrders(call(r), &orderv1.ListOrdersRequest{PageSize: size, PageToken: tok})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

func (s *Server) getOrder(w http.ResponseWriter, r *http.Request) {
	resp, err := s.c.Order.GetOrder(call(r), &orderv1.GetOrderRequest{Id: chi.URLParam(r, "id")})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

func (s *Server) cancelOrder(w http.ResponseWriter, r *http.Request) {
	resp, err := s.c.Order.CancelOrder(call(r), &orderv1.CancelOrderRequest{Id: chi.URLParam(r, "id")})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

type payBody struct {
	PaymentMethodToken string `json:"payment_method_token"`
}

func (s *Server) payOrder(w http.ResponseWriter, r *http.Request) {
	var b payBody
	if !decode(w, r, &b) {
		return
	}
	resp, err := s.c.Payment.PayOrder(call(r), &paymentv1.PayOrderRequest{
		OrderId: chi.URLParam(r, "id"), PaymentMethodToken: b.PaymentMethodToken,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	code := http.StatusOK
	if resp.GetPayment().GetStatus() == paymentv1.PaymentStatus_PAYMENT_STATUS_FAILED {
		code = http.StatusPaymentRequired
	}
	writeProto(w, code, resp)
}

func (s *Server) getPayment(w http.ResponseWriter, r *http.Request) {
	resp, err := s.c.Payment.GetPaymentByOrder(call(r), &paymentv1.GetPaymentByOrderRequest{OrderId: chi.URLParam(r, "id")})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

// --- admin -----------------------------------------------------------------

type createProductBody struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	PriceCents   int64  `json:"price_cents"`
	ImageURL     string `json:"image_url"`
	InitialStock int32  `json:"initial_stock"`
}

func (s *Server) adminCreateProduct(w http.ResponseWriter, r *http.Request) {
	var b createProductBody
	if !decode(w, r, &b) {
		return
	}
	resp, err := s.c.Product.CreateProduct(call(r), &productv1.CreateProductRequest{
		Name: b.Name, Description: b.Description, PriceCents: b.PriceCents, ImageUrl: b.ImageURL, InitialStock: b.InitialStock,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusCreated, resp)
}

type updateProductBody struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	PriceCents  *int64  `json:"price_cents"`
	ImageURL    *string `json:"image_url"`
	IsActive    *bool   `json:"is_active"`
}

func (s *Server) adminUpdateProduct(w http.ResponseWriter, r *http.Request) {
	var b updateProductBody
	if !decode(w, r, &b) {
		return
	}
	resp, err := s.c.Product.UpdateProduct(call(r), &productv1.UpdateProductRequest{
		Id: chi.URLParam(r, "id"), Name: b.Name, Description: b.Description,
		PriceCents: b.PriceCents, ImageUrl: b.ImageURL, IsActive: b.IsActive,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

func (s *Server) adminDeleteProduct(w http.ResponseWriter, r *http.Request) {
	if _, err := s.c.Product.DeleteProduct(call(r), &productv1.DeleteProductRequest{Id: chi.URLParam(r, "id")}); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type setStockBody struct {
	Available int32 `json:"available"`
}

func (s *Server) adminSetStock(w http.ResponseWriter, r *http.Request) {
	var b setStockBody
	if !decode(w, r, &b) {
		return
	}
	resp, err := s.c.Inventory.SetStock(call(r), &inventoryv1.SetStockRequest{
		ProductId: chi.URLParam(r, "productId"), Available: b.Available,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

type adjustStockBody struct {
	Delta int32 `json:"delta"`
}

func (s *Server) adminAdjustStock(w http.ResponseWriter, r *http.Request) {
	var b adjustStockBody
	if !decode(w, r, &b) {
		return
	}
	resp, err := s.c.Inventory.AdjustStock(call(r), &inventoryv1.AdjustStockRequest{
		ProductId: chi.URLParam(r, "productId"), Delta: b.Delta,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

type orderStatusBody struct {
	Status string `json:"status"`
}

func (s *Server) adminUpdateOrderStatus(w http.ResponseWriter, r *http.Request) {
	var b orderStatusBody
	if !decode(w, r, &b) {
		return
	}
	st, ok := orderv1.OrderStatus_value["ORDER_STATUS_"+strings.ToUpper(strings.TrimSpace(b.Status))]
	if !ok {
		writeProblem(w, r, http.StatusBadRequest, "status must be SHIPPED or DELIVERED")
		return
	}
	resp, err := s.c.Order.UpdateOrderStatus(call(r), &orderv1.UpdateOrderStatusRequest{
		Id: chi.URLParam(r, "id"), Status: orderv1.OrderStatus(st),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}
