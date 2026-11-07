package main

import (
	"log"
	"net"

	"google.golang.org/grpc"

	"github.com/ayushdevani/forgecart/gen"
	"github.com/ayushdevani/forgecart/pkg/config"
	"github.com/ayushdevani/forgecart/services/inventory-service/handler"
	"github.com/ayushdevani/forgecart/services/inventory-service/repository"
	"github.com/ayushdevani/forgecart/services/inventory-service/service"
)

func main() {
	port := config.GetEnv("INVENTORY_SERVICE_PORT", "50056")
	dbURL := config.GetEnv("INVENTORY_DB_URL", "postgres://forgecart:forgecart_pass@localhost:5432/inventory_db?sslmode=disable")

	repo, err := repository.NewInventoryRepository(dbURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer repo.Close()

	svc := service.NewInventoryService(repo)
	h := handler.NewInventoryHandler(svc)

	grpcServer := grpc.NewServer()
	gen.RegisterInventoryServiceServer(grpcServer, h)

	listener, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	log.Printf("Inventory Service starting on port %s", port)
	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
