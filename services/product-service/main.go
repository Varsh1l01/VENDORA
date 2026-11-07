package main

import (
	"log"
	"net"

	"google.golang.org/grpc"

	"github.com/ayushdevani/forgecart/gen"
	"github.com/ayushdevani/forgecart/pkg/config"
	"github.com/ayushdevani/forgecart/services/product-service/handler"
	"github.com/ayushdevani/forgecart/services/product-service/repository"
	"github.com/ayushdevani/forgecart/services/product-service/service"
)

func main() {
	port := config.GetEnv("PRODUCT_SERVICE_PORT", "50052")
	dbURL := config.GetEnv("PRODUCT_DB_URL", "postgres://forgecart:forgecart_pass@localhost:5432/products_db?sslmode=disable")

	repo, err := repository.NewProductRepository(dbURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer repo.Close()

	svc := service.NewProductService(repo)
	h := handler.NewProductHandler(svc)

	grpcServer := grpc.NewServer()
	gen.RegisterProductServiceServer(grpcServer, h)

	listener, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	log.Printf("Product Service starting on port %s", port)
	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
