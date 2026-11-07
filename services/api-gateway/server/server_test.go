package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cartv1 "github.com/Varsh1l01/VENDORA/gen/vendora/cart/v1"
	orderv1 "github.com/Varsh1l01/VENDORA/gen/vendora/order/v1"
	productv1 "github.com/Varsh1l01/VENDORA/gen/vendora/product/v1"
	"github.com/Varsh1l01/VENDORA/pkg/auth"
)

const testSecret = "gateway-test-secret-0123456789abcdef"

type fakeCart struct {
	cartv1.CartServiceClient
	lastAdd *cartv1.AddItemRequest
}

func (f *fakeCart) GetCart(context.Context, *cartv1.GetCartRequest, ...grpc.CallOption) (*cartv1.GetCartResponse, error) {
	return &cartv1.GetCartResponse{Cart: &cartv1.Cart{UserId: "u1", TotalCents: 0, Currency: "USD"}}, nil
}

func (f *fakeCart) AddItem(_ context.Context, r *cartv1.AddItemRequest, _ ...grpc.CallOption) (*cartv1.AddItemResponse, error) {
	f.lastAdd = r
	return &cartv1.AddItemResponse{Cart: &cartv1.Cart{}}, nil
}

type fakeOrder struct {
	orderv1.OrderServiceClient
	err     error
	lastKey string
	lastCtx context.Context
}

func (f *fakeOrder) CreateOrder(ctx context.Context, r *orderv1.CreateOrderRequest, _ ...grpc.CallOption) (*orderv1.CreateOrderResponse, error) {
	f.lastKey, f.lastCtx = r.GetIdempotencyKey(), ctx
	if f.err != nil {
		return nil, f.err
	}
	return &orderv1.CreateOrderResponse{Order: &orderv1.Order{Id: "o1", Status: orderv1.OrderStatus_ORDER_STATUS_AWAITING_PAYMENT}}, nil
}

func (f *fakeOrder) GetOrder(context.Context, *orderv1.GetOrderRequest, ...grpc.CallOption) (*orderv1.GetOrderResponse, error) {
	return nil, status.Error(codes.Internal, "pq: connection refused at 10.0.0.5:5432")
}

type fakeProduct struct{ productv1.ProductServiceClient }

func (fakeProduct) CreateProduct(context.Context, *productv1.CreateProductRequest, ...grpc.CallOption) (*productv1.CreateProductResponse, error) {
	return &productv1.CreateProductResponse{Product: &productv1.Product{Id: "p1"}}, nil
}

type harness struct {
	h      http.Handler
	tokens *auth.Tokens
	cart   *fakeCart
	order  *fakeOrder
}

func newHarness() *harness {
	tk := auth.NewTokens(testSecret, "vendora-user-service", "vendora")
	cart, order := &fakeCart{}, &fakeOrder{}
	s := New(Config{CORSOrigins: []string{"https://shop.example"}, RequestTimeout: time.Second},
		tk, Clients{Cart: cart, Order: order, Product: fakeProduct{}}, nil)
	return &harness{h: s.Handler(), tokens: tk, cart: cart, order: order}
}

func (h *harness) token(t *testing.T, role string) string {
	tok, _, err := h.tokens.Issue(auth.Principal{Subject: "u1", Role: role}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func (h *harness) do(method, path, body, token string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	return rec
}

func problem(t *testing.T, rec *httptest.ResponseRecorder) Problem {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content-type = %q, body=%s", ct, rec.Body)
	}
	var p Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAuthRequired(t *testing.T) {
	h := newHarness()
	rec := h.do("GET", "/api/v1/cart", "", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d", rec.Code)
	}
	if p := problem(t, rec); p.RequestID == "" || rec.Header().Get("X-Request-ID") != p.RequestID {
		t.Fatalf("request id missing/mismatched: %+v", p)
	}
	if rec := h.do("GET", "/api/v1/cart", "", "garbage", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad token code = %d", rec.Code)
	}
}

func TestServiceTokensRejectedAtEdge(t *testing.T) {
	h := newHarness()
	tok, _, _ := h.tokens.Issue(auth.Principal{Subject: "order-service", Role: auth.RoleService}, time.Minute)
	if rec := h.do("GET", "/api/v1/cart", "", tok, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("service token accepted at the edge: %d", rec.Code)
	}
}

func TestAdminRoutesRequireAdmin(t *testing.T) {
	h := newHarness()
	body := `{"name":"x","price_cents":100}`
	if rec := h.do("POST", "/api/v1/admin/products", body, h.token(t, auth.RoleCustomer), nil); rec.Code != http.StatusForbidden {
		t.Fatalf("customer got %d", rec.Code)
	}
	if rec := h.do("POST", "/api/v1/admin/products", body, h.token(t, auth.RoleAdmin), nil); rec.Code != http.StatusCreated {
		t.Fatalf("admin got %d: %s", rec.Code, rec.Body)
	}
}

func TestZeroValuesAreSerialized(t *testing.T) {
	h := newHarness()
	rec := h.do("GET", "/api/v1/cart", "", h.token(t, auth.RoleCustomer), nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"total_cents":"0"`) {
		t.Fatalf("zero total missing: %d %s", rec.Code, rec.Body)
	}
}

func TestStrictJSONAndValidation(t *testing.T) {
	h := newHarness()
	tok := h.token(t, auth.RoleCustomer)
	cases := map[string]string{
		"unknown field":     `{"product_id":"p","quantity":1,"price":0}`,
		"negative quantity": `{"product_id":"p","quantity":-5}`,
		"zero quantity":     `{"product_id":"p","quantity":0}`,
		"wrong type":        `{"product_id":"p","quantity":"1"}`,
		"malformed":         `{"product_id":`,
		"two objects":       `{"product_id":"p","quantity":1}{}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			rec := h.do("POST", "/api/v1/cart/items", body, tok, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("code = %d body=%s", rec.Code, rec.Body)
			}
			problem(t, rec)
		})
	}
	if h.cart.lastAdd != nil {
		t.Fatal("invalid requests must not reach the cart service")
	}
	big := `{"product_id":"` + strings.Repeat("a", MaxBodyBytes) + `","quantity":1}`
	if rec := h.do("POST", "/api/v1/cart/items", big, tok, nil); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: %d", rec.Code)
	}
}

func TestCreateOrderRequiresIdempotencyKeyAndForwardsToken(t *testing.T) {
	h := newHarness()
	tok := h.token(t, auth.RoleCustomer)
	if rec := h.do("POST", "/api/v1/orders", "", tok, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing key: %d", rec.Code)
	}
	rec := h.do("POST", "/api/v1/orders", `{"shipping_address":"1 Main"}`, tok, map[string]string{"Idempotency-Key": "abc"})
	if rec.Code != http.StatusCreated || h.order.lastKey != "abc" {
		t.Fatalf("code=%d key=%q body=%s", rec.Code, h.order.lastKey, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"ORDER_STATUS_AWAITING_PAYMENT"`) {
		t.Fatalf("body = %s", rec.Body)
	}
}

func TestGRPCErrorsMapToHTTPWithoutLeaks(t *testing.T) {
	h := newHarness()
	tok := h.token(t, auth.RoleCustomer)
	h.order.err = status.Error(codes.FailedPrecondition, "insufficient stock for product p1")
	rec := h.do("POST", "/api/v1/orders", "", tok, map[string]string{"Idempotency-Key": "k"})
	if rec.Code != http.StatusUnprocessableEntity || problem(t, rec).Detail != "insufficient stock for product p1" {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	rec = h.do("GET", "/api/v1/orders/o1", "", tok, nil)
	p := problem(t, rec)
	if rec.Code != http.StatusInternalServerError || strings.Contains(p.Detail, "10.0.0.5") {
		t.Fatalf("internal details leaked: %d %+v", rec.Code, p)
	}
}

func TestHTTPStatusMapping(t *testing.T) {
	want := map[codes.Code]int{
		codes.InvalidArgument: 400, codes.Unauthenticated: 401, codes.PermissionDenied: 403,
		codes.NotFound: 404, codes.AlreadyExists: 409, codes.FailedPrecondition: 422,
		codes.ResourceExhausted: 429, codes.Unavailable: 503, codes.DeadlineExceeded: 504, codes.Internal: 500,
	}
	for c, s := range want {
		if got := HTTPStatus(c); got != s {
			t.Errorf("%v -> %d, want %d", c, got, s)
		}
	}
}

func TestCORSAllowlist(t *testing.T) {
	h := newHarness()
	rec := h.do("OPTIONS", "/api/v1/products", "", "", map[string]string{
		"Origin": "https://evil.example", "Access-Control-Request-Method": "GET",
	})
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("unlisted origin must not be allowed")
	}
	rec = h.do("OPTIONS", "/api/v1/products", "", "", map[string]string{
		"Origin": "https://shop.example", "Access-Control-Request-Method": "GET",
	})
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != "https://shop.example" {
		t.Fatalf("allowed origin: %d %v", rec.Code, rec.Header())
	}
}

func TestSecurityHeadersAndUnknownRoute(t *testing.T) {
	h := newHarness()
	rec := h.do("GET", "/api/v1/nope", "", "", nil)
	if rec.Code != http.StatusNotFound || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("code=%d headers=%v", rec.Code, rec.Header())
	}
}
