package main

import (
	"log"
	"net"

	"google.golang.org/grpc"

	"github.com/ayushdevani/forgecart/gen"
	"github.com/ayushdevani/forgecart/pkg/config"
	"github.com/ayushdevani/forgecart/services/cart-service/handler"
	"github.com/ayushdevani/forgecart/services/cart-service/repository"
	"github.com/ayushdevani/forgecart/services/cart-service/service"
)

func main() {
	port := config.GetEnv("CART_SERVICE_PORT", "50053")
	redisURL := config.GetEnv("REDIS_URL", "localhost:6379")

	repo, err := repository.NewCartRepository(redisURL)
	if err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	defer repo.Close()

	svc := service.NewCartService(repo)
	h := handler.NewCartHandler(svc)

	grpcServer := grpc.NewServer()
	gen.RegisterCartServiceServer(grpcServer, h)

	listener, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	log.Printf("Cart Service starting on port %s", port)
	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
