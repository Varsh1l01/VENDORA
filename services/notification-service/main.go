package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ayushdevani/forgecart/pkg/config"
	"github.com/ayushdevani/forgecart/pkg/kafka"
	"github.com/ayushdevani/forgecart/services/notification-service/email"
)

// Event data structures
type UserRegisteredData struct {
	UserID    string `json:"user_id"`
	Email     string `json:"email"`
	FirstName string `json:"first_name"`
}

type OrderCreatedData struct {
	OrderID string  `json:"order_id"`
	UserID  string  `json:"user_id"`
	Email   string  `json:"email"`
	Total   float64 `json:"total"`
}

type OrderStatusUpdatedData struct {
	OrderID string `json:"order_id"`
	UserID  string `json:"user_id"`
	Email   string `json:"email"`
	Status  string `json:"status"`
}

type PaymentCompletedData struct {
	PaymentID string  `json:"payment_id"`
	OrderID   string  `json:"order_id"`
	UserID    string  `json:"user_id"`
	Email     string  `json:"email"`
	Amount    float64 `json:"amount"`
}

func main() {
	brokers := config.GetEnv("KAFKA_BROKERS", "localhost:9092")
	topic := config.GetEnv("KAFKA_TOPIC", "notifications")
	groupID := config.GetEnv("KAFKA_GROUP_ID", "notification-service")

	emailSvc := email.NewEmailService()
	consumer := kafka.NewConsumer([]string{brokers}, topic, groupID)
	defer consumer.Close()

	ctx, cancel := context.WithCancel(context.Background())

	// Graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		log.Println("Shutting down Notification Service...")
		cancel()
	}()

	log.Printf("Notification Service started, consuming from topic: %s", topic)

	err := consumer.Consume(ctx, func(ctx context.Context, event kafka.Event) error {
		return handleEvent(emailSvc, event)
	})

	if err != nil && err != context.Canceled {
		log.Fatalf("Consumer error: %v", err)
	}
}

func handleEvent(emailSvc *email.EmailService, event kafka.Event) error {
	log.Printf("[EVENT] Type: %s", event.Type)

	switch event.Type {
	case "user.registered":
		var data UserRegisteredData
		if err := unmarshalData(event.Data, &data); err != nil {
			return err
		}
		return emailSvc.SendWelcome(data.Email, data.FirstName)

	case "order.created":
		var data OrderCreatedData
		if err := unmarshalData(event.Data, &data); err != nil {
			return err
		}
		return emailSvc.SendOrderConfirmation(data.Email, data.OrderID, data.Total)

	case "order.status.updated":
		var data OrderStatusUpdatedData
		if err := unmarshalData(event.Data, &data); err != nil {
			return err
		}
		return emailSvc.SendOrderStatusUpdate(data.Email, data.OrderID, data.Status)

	case "payment.completed":
		var data PaymentCompletedData
		if err := unmarshalData(event.Data, &data); err != nil {
			return err
		}
		return emailSvc.SendPaymentReceipt(data.Email, data.PaymentID, data.OrderID, data.Amount)

	default:
		log.Printf("[WARN] Unknown event type: %s", event.Type)
	}

	return nil
}

func unmarshalData(data interface{}, target interface{}) error {
	bytes, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return json.Unmarshal(bytes, target)
}
