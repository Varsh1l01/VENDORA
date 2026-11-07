package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	httpSwagger "github.com/swaggo/http-swagger"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/ayushdevani/forgecart/gen"
	"github.com/ayushdevani/forgecart/pkg/config"
	"github.com/ayushdevani/forgecart/pkg/health"
	authMw "github.com/ayushdevani/forgecart/pkg/middleware"
	"github.com/ayushdevani/forgecart/services/api-gateway/handlers"

	_ "github.com/ayushdevani/forgecart/services/api-gateway/docs"
)

// @title           ForgeCart API
// @version         1.0
// @description     E-commerce microservices backend API. This gateway routes requests to individual microservices via gRPC.
// @description
// @description     ## Architecture
// @description     - **user-service**: Authentication and user management
// @description     - **product-service**: Product catalog management
// @description     - **cart-service**: Shopping cart operations
// @description     - **order-service**: Order processing
// @description     - **payment-service**: Payment processing
// @description     - **inventory-service**: Stock management

// @host            shop.launch-pad.dev
// @schemes         https
// @BasePath        /

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Enter your JWT token with the `Bearer ` prefix, e.g. `Bearer eyJhbGciOiJIUzI1NiIs...`

func main() {
	port := config.GetEnv("API_GATEWAY_PORT", "8080")
	jwtSecret := config.GetEnv("JWT_SECRET", "super-secret-jwt-key")

	userConn := mustConnect(config.GetEnv("USER_SERVICE_ADDR", "localhost:50051"))
	productConn := mustConnect(config.GetEnv("PRODUCT_SERVICE_ADDR", "localhost:50052"))
	cartConn := mustConnect(config.GetEnv("CART_SERVICE_ADDR", "localhost:50053"))
	orderConn := mustConnect(config.GetEnv("ORDER_SERVICE_ADDR", "localhost:50054"))
	paymentConn := mustConnect(config.GetEnv("PAYMENT_SERVICE_ADDR", "localhost:50055"))
	inventoryConn := mustConnect(config.GetEnv("INVENTORY_SERVICE_ADDR", "localhost:50056"))

	defer userConn.Close()
	defer productConn.Close()
	defer cartConn.Close()
	defer orderConn.Close()
	defer paymentConn.Close()
	defer inventoryConn.Close()

	clients := &handlers.Clients{
		User:      gen.NewUserServiceClient(userConn),
		Product:   gen.NewProductServiceClient(productConn),
		Cart:      gen.NewCartServiceClient(cartConn),
		Order:     gen.NewOrderServiceClient(orderConn),
		Payment:   gen.NewPaymentServiceClient(paymentConn),
		Inventory: gen.NewInventoryServiceClient(inventoryConn),
	}

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware)

	r.Get("/health", health.Handler("api-gateway"))

	// Swagger UI
	r.Get("/swagger/*", httpSwagger.Handler(
		httpSwagger.URL("/swagger/doc.json"),
	))

	// Auth
	r.Post("/api/auth/register", handlers.Register(clients.User))
	r.Post("/api/auth/login", handlers.Login(clients.User))

	r.Get("/api/products", handlers.ListProducts(clients.Product))
	r.Get("/api/products/{id}", handlers.GetProduct(clients.Product))

	// Inventory
	r.Get("/api/inventory/{productId}", handlers.GetStock(clients.Inventory))

	// Protected routes
	r.Group(func(r chi.Router) {
		r.Use(authMw.Auth(jwtSecret))

		// Cart
		r.Get("/api/cart", handlers.GetCart(clients.Cart))
		r.Post("/api/cart/items", handlers.AddToCart(clients.Cart))
		r.Delete("/api/cart/items/{productId}", handlers.RemoveFromCart(clients.Cart))

		// Orders
		r.Post("/api/orders", handlers.CreateOrder(clients.Order))
		r.Get("/api/orders", handlers.ListOrders(clients.Order))
		r.Get("/api/orders/{id}", handlers.GetOrder(clients.Order))

		r.Post("/api/payments", handlers.CreatePayment(clients.Payment))
		r.Get("/api/payments/order/{orderId}", handlers.GetPayment(clients.Payment))
	})

	// Admin-only routes
	r.Group(func(r chi.Router) {
		r.Use(authMw.Auth(jwtSecret))
		r.Use(authMw.RequireAdmin)

		r.Post("/api/products", handlers.CreateProduct(clients.Product))
	})

	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		log.Println("Shutting down API Gateway...")
		os.Exit(0)
	}()

	log.Printf("API Gateway starting on port %s", port)
	log.Printf("Swagger UI available at http://localhost:%s/swagger/index.html", port)
	log.Fatal(http.ListenAndServe(":"+port, r))
}

func mustConnect(addr string) *grpc.ClientConn {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to %s: %v", addr, err)
	}
	log.Printf("Connected to %s", addr)
	return conn
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowedOrigins := strings.Split(config.GetEnv("CORS_ORIGINS", "*"), ",")

		for _, allowed := range allowedOrigins {
			if allowed == "*" || allowed == origin {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				break
			}
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
