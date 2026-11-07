package main

import (
	"log"
	"net"
	"strings"

	"google.golang.org/grpc"

	"github.com/ayushdevani/forgecart/gen"
	"github.com/ayushdevani/forgecart/pkg/config"
	"github.com/ayushdevani/forgecart/pkg/kafka"
	"github.com/ayushdevani/forgecart/services/user-service/handler"
	"github.com/ayushdevani/forgecart/services/user-service/repository"
	"github.com/ayushdevani/forgecart/services/user-service/service"
)

func main() {
	port := config.GetEnv("USER_SERVICE_PORT", "50051")
	dbURL := config.GetEnv("USER_DB_URL", "postgres://forgecart:forgecart_pass@localhost:5432/users_db?sslmode=disable")
	jwtSecret := config.GetEnv("JWT_SECRET", "super-secret-jwt-key")
	kafkaBrokers := config.GetEnv("KAFKA_BROKERS", "localhost:9092")
	kafkaTopic := config.GetEnv("KAFKA_TOPIC", "notifications")

	repo, err := repository.NewUserRepository(dbURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer repo.Close()

	var producer *kafka.Producer
	if kafkaBrokers != "" {
		producer = kafka.NewProducer(strings.Split(kafkaBrokers, ","), kafkaTopic)
		defer producer.Close()
		log.Printf("Kafka producer connected to %s", kafkaBrokers)
	}

	svc := service.NewUserService(repo, jwtSecret, producer)
	h := handler.NewUserHandler(svc)

	grpcServer := grpc.NewServer()
	gen.RegisterUserServiceServer(grpcServer, h)

	listener, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	log.Printf("User Service starting on port %s", port)
	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
