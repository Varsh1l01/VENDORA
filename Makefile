
.PHONY: proto
proto:
	@echo "Generating Go code from proto files..."
	buf generate
	@echo "Proto generation complete!"

.PHONY: proto-lint
proto-lint:
	buf lint

.PHONY: swagger
swagger:
	@echo "Generating Swagger docs..."
	cd services/api-gateway && swag init -g main.go -o docs
	@echo "Swagger docs generated!"

.PHONY: infra-up
infra-up:
	@echo "Starting infrastructure..."
	docker-compose up -d postgres redis zookeeper kafka kafka-ui
	@echo "Infrastructure started!"
	@echo "Kafka UI: http://localhost:8090"

.PHONY: up
up:
	docker-compose up -d --build

.PHONY: down
down:
	docker-compose down

.PHONY: clean
clean:
	docker-compose down -v
	@echo "All containers and volumes removed!"

.PHONY: logs
logs:
	docker-compose logs -f $(SERVICE)

.PHONY: run-user
run-user:
	cd services/user-service && go run .

.PHONY: run-product
run-product:
	cd services/product-service && go run .

.PHONY: run-cart
run-cart:
	cd services/cart-service && go run .

.PHONY: run-order
run-order:
	cd services/order-service && go run .

.PHONY: run-payment
run-payment:
	cd services/payment-service && go run .

.PHONY: run-inventory
run-inventory:
	cd services/inventory-service && go run .

.PHONY: run-notification
run-notification:
	cd services/notification-service && go run .

.PHONY: run-gateway
run-gateway:
	cd services/api-gateway && go run .

.PHONY: test
test:
	go test ./... -v

.PHONY: deps
deps:
	go install github.com/bufbuild/buf/cmd/buf@latest
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	@echo "All dependencies installed!"


.PHONY: tidy
tidy:
	cd pkg && go mod tidy
	cd services/user-service && go mod tidy
	cd services/product-service && go mod tidy
	cd services/cart-service && go mod tidy
	cd services/order-service && go mod tidy
	cd services/payment-service && go mod tidy
	cd services/inventory-service && go mod tidy
	cd services/notification-service && go mod tidy
	cd services/api-gateway && go mod tidy


# docs
.PHONY: help
help:
	@echo "ForgeCart Makefile Commands:"
	@echo ""
	@echo "  make proto          - Generate Go code from .proto files"
	@echo "  make infra-up       - Start infrastructure (Postgres, Redis, Kafka)"
	@echo "  make up             - Start everything"
	@echo "  make down           - Stop everything"
	@echo "  make clean          - Stop and remove all data"
	@echo "  make run-user       - Run user-service locally"
	@echo "  make test           - Run all tests"
	@echo "  make deps           - Install development dependencies"
