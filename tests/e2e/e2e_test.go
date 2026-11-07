//go:build e2e

// Package e2e exercises a running VENDORA stack through the public API.
//
//	make up
//	VENDORA_ADMIN_EMAIL=... VENDORA_ADMIN_PASSWORD=... go test -tags e2e ./tests/e2e -v
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

var base = env("VENDORA_BASE_URL", "http://localhost:8080") + "/api/v1"

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

type resp struct {
	code int
	body map[string]any
	raw  string
}

func call(t *testing.T, method, path, token string, body any, hdr ...string) resp {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, base+path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	r := resp{code: res.StatusCode, raw: string(raw)}
	_ = json.Unmarshal(raw, &r.body)
	return r
}

func must(t *testing.T, r resp, code int) resp {
	t.Helper()
	if r.code != code {
		t.Fatalf("expected %d, got %d: %s", code, r.code, r.raw)
	}
	return r
}

// get walks a decoded JSON object: get(m, "order", "status").
func get(m map[string]any, path ...string) any {
	var cur any = m
	for _, p := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[p]
	}
	return cur
}

func str(v any) string { s, _ := v.(string); return s }

func num(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case string: // protojson renders int64 as string
		n, _ := strconv.ParseInt(x, 10, 64)
		return n
	}
	return 0
}

func eventually(t *testing.T, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func login(t *testing.T, email, password string) string {
	r := must(t, call(t, "POST", "/auth/login", "", map[string]string{"email": email, "password": password}), 200)
	return str(get(r.body, "tokens", "access_token"))
}

func register(t *testing.T) (string, string) {
	email := fmt.Sprintf("e2e-%s@example.com", uuid.NewString()[:8])
	r := must(t, call(t, "POST", "/auth/register", "", map[string]string{
		"email": email, "password": "correct-horse-battery", "first_name": "E2E", "last_name": "User",
	}), 201)
	return str(get(r.body, "tokens", "access_token")), str(get(r.body, "user", "id"))
}

func createProduct(t *testing.T, admin string, price, stock int) string {
	r := must(t, call(t, "POST", "/admin/products", admin, map[string]any{
		"name": "E2E product " + uuid.NewString()[:6], "price_cents": price, "initial_stock": stock,
	}), 201)
	id := str(get(r.body, "product", "id"))
	eventually(t, "inventory provisioned from product.created", func() bool {
		s := call(t, "GET", "/products/"+id+"/stock", "", nil)
		return s.code == 200 && num(get(s.body, "stock", "available")) == int64(stock)
	})
	return id
}

func stock(t *testing.T, admin, id string) (int64, int64) {
	s := must(t, call(t, "GET", "/products/"+id+"/stock", admin, nil), 200)
	return num(get(s.body, "stock", "available")), num(get(s.body, "stock", "reserved"))
}

func orderStatus(t *testing.T, tok, id string) string {
	return str(get(must(t, call(t, "GET", "/orders/"+id, tok, nil), 200).body, "order", "status"))
}

func adminToken(t *testing.T) string {
	email, pw := os.Getenv("VENDORA_ADMIN_EMAIL"), os.Getenv("VENDORA_ADMIN_PASSWORD")
	if email == "" || pw == "" {
		t.Skip("set VENDORA_ADMIN_EMAIL and VENDORA_ADMIN_PASSWORD (from .env)")
	}
	return login(t, email, pw)
}

func TestCheckoutHappyPath(t *testing.T) {
	admin := adminToken(t)
	pid := createProduct(t, admin, 2999, 5)
	user, _ := register(t)

	must(t, call(t, "POST", "/cart/items", user, map[string]any{"product_id": pid, "quantity": -3}), 400)
	cart := must(t, call(t, "POST", "/cart/items", user, map[string]any{"product_id": pid, "quantity": 2}), 200)
	if got := num(get(cart.body, "cart", "total_cents")); got != 5998 {
		t.Fatalf("cart total = %d, want 5998", got)
	}

	key := uuid.NewString()
	o := must(t, call(t, "POST", "/orders", user, map[string]any{"shipping_address": "1 Main St"}, "Idempotency-Key", key), 201)
	oid := str(get(o.body, "order", "id"))
	if s := str(get(o.body, "order", "status")); s != "ORDER_STATUS_AWAITING_PAYMENT" {
		t.Fatalf("status = %s", s)
	}
	if got := num(get(o.body, "order", "total_cents")); got != 5998 {
		t.Fatalf("order total = %d", got)
	}
	again := must(t, call(t, "POST", "/orders", user, nil, "Idempotency-Key", key), 201)
	if str(get(again.body, "order", "id")) != oid {
		t.Fatal("same Idempotency-Key produced a different order")
	}
	if a, r := stock(t, admin, pid); a != 3 || r != 2 {
		t.Fatalf("after reserve: available=%d reserved=%d", a, r)
	}
	eventually(t, "cart emptied by order.created", func() bool {
		c := call(t, "GET", "/cart", user, nil)
		items, _ := get(c.body, "cart", "items").([]any)
		return c.code == 200 && len(items) == 0
	})

	other, _ := register(t)
	must(t, call(t, "GET", "/orders/"+oid, other, nil), 404)
	must(t, call(t, "GET", "/orders/"+oid+"/payment", other, nil), 404)
	must(t, call(t, "POST", "/orders/"+oid+"/payment", other, map[string]string{"payment_method_token": "tok_visa"}), 404)

	p := must(t, call(t, "POST", "/orders/"+oid+"/payment", user, map[string]string{"payment_method_token": "tok_visa"}), 200)
	if s := str(get(p.body, "payment", "status")); s != "PAYMENT_STATUS_SUCCEEDED" {
		t.Fatalf("payment status = %s", s)
	}
	if got := num(get(p.body, "payment", "amount_cents")); got != 5998 {
		t.Fatalf("charged %d, want 5998", got)
	}
	p2 := must(t, call(t, "POST", "/orders/"+oid+"/payment", user, map[string]string{"payment_method_token": "tok_visa"}), 200)
	if str(get(p2.body, "payment", "id")) != str(get(p.body, "payment", "id")) {
		t.Fatal("paying twice created a second payment")
	}

	eventually(t, "order confirmed by payment.succeeded", func() bool {
		return orderStatus(t, user, oid) == "ORDER_STATUS_CONFIRMED"
	})
	eventually(t, "reservation committed by order.confirmed", func() bool {
		a, r := stock(t, admin, pid)
		return a == 3 && r == 0
	})

	must(t, call(t, "PATCH", "/admin/orders/"+oid+"/status", user, map[string]string{"status": "SHIPPED"}), 403)
	must(t, call(t, "PATCH", "/admin/orders/"+oid+"/status", admin, map[string]string{"status": "SHIPPED"}), 200)
	must(t, call(t, "PATCH", "/admin/orders/"+oid+"/status", admin, map[string]string{"status": "DELIVERED"}), 200)
	must(t, call(t, "POST", "/orders/"+oid+"/cancel", user, nil), 422)
}

func TestDeclinedPaymentReleasesStock(t *testing.T) {
	admin := adminToken(t)
	pid := createProduct(t, admin, 1000, 4)
	user, _ := register(t)
	must(t, call(t, "POST", "/cart/items", user, map[string]any{"product_id": pid, "quantity": 3}), 200)
	o := must(t, call(t, "POST", "/orders", user, nil, "Idempotency-Key", uuid.NewString()), 201)
	oid := str(get(o.body, "order", "id"))

	p := must(t, call(t, "POST", "/orders/"+oid+"/payment", user, map[string]string{"payment_method_token": "tok_decline"}), 402)
	if s := str(get(p.body, "payment", "status")); s != "PAYMENT_STATUS_FAILED" {
		t.Fatalf("payment status = %s", s)
	}
	eventually(t, "order cancelled by payment.failed", func() bool {
		return orderStatus(t, user, oid) == "ORDER_STATUS_CANCELLED"
	})
	eventually(t, "stock released by order.cancelled", func() bool {
		a, r := stock(t, admin, pid)
		return a == 4 && r == 0
	})
}

func TestUserCancelReleasesStockOnce(t *testing.T) {
	admin := adminToken(t)
	pid := createProduct(t, admin, 500, 10)
	user, _ := register(t)
	must(t, call(t, "POST", "/cart/items", user, map[string]any{"product_id": pid, "quantity": 4}), 200)
	o := must(t, call(t, "POST", "/orders", user, nil, "Idempotency-Key", uuid.NewString()), 201)
	oid := str(get(o.body, "order", "id"))
	must(t, call(t, "POST", "/orders/"+oid+"/cancel", user, nil), 200)
	must(t, call(t, "POST", "/orders/"+oid+"/cancel", user, nil), 422)
	eventually(t, "stock released exactly once", func() bool {
		a, r := stock(t, admin, pid)
		return a == 10 && r == 0
	})
	time.Sleep(2 * time.Second)
	if a, r := stock(t, admin, pid); a != 10 || r != 0 {
		t.Fatalf("stock drifted after duplicate cancel: available=%d reserved=%d", a, r)
	}
}

func TestConcurrentCheckoutNeverOversells(t *testing.T) {
	admin := adminToken(t)
	const stockQty, buyers = 5, 12
	pid := createProduct(t, admin, 100, stockQty)
	tokens := make([]string, buyers)
	for i := range tokens {
		tokens[i], _ = register(t)
		must(t, call(t, "POST", "/cart/items", tokens[i], map[string]any{"product_id": pid, "quantity": 1}), 200)
	}
	var wg sync.WaitGroup
	codes := make([]int, buyers)
	for i, tok := range tokens {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = call(t, "POST", "/orders", tok, nil, "Idempotency-Key", uuid.NewString()).code
		}()
	}
	wg.Wait()
	ok, rejected := 0, 0
	for _, c := range codes {
		switch c {
		case 201:
			ok++
		case 422:
			rejected++
		default:
			t.Errorf("unexpected status %d", c)
		}
	}
	if ok != stockQty || rejected != buyers-stockQty {
		t.Fatalf("succeeded=%d rejected=%d, want %d/%d", ok, rejected, stockQty, buyers-stockQty)
	}
	if a, r := stock(t, admin, pid); a != 0 || r != stockQty {
		t.Fatalf("available=%d reserved=%d", a, r)
	}
}

func TestRefreshTokenRotationAndReuseDetection(t *testing.T) {
	email := fmt.Sprintf("e2e-%s@example.com", uuid.NewString()[:8])
	r := must(t, call(t, "POST", "/auth/register", "", map[string]string{
		"email": email, "password": "correct-horse-battery", "first_name": "R", "last_name": "T",
	}), 201)
	rt1 := str(get(r.body, "tokens", "refresh_token"))
	r2 := must(t, call(t, "POST", "/auth/refresh", "", map[string]string{"refresh_token": rt1}), 200)
	rt2 := str(get(r2.body, "tokens", "refresh_token"))
	// Reusing the old token is treated as theft: everything is revoked.
	must(t, call(t, "POST", "/auth/refresh", "", map[string]string{"refresh_token": rt1}), 401)
	must(t, call(t, "POST", "/auth/refresh", "", map[string]string{"refresh_token": rt2}), 401)
}
