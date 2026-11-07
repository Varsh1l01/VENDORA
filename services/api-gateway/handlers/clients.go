package handlers

import "github.com/ayushdevani/forgecart/gen"

type Clients struct {
	User      gen.UserServiceClient
	Product   gen.ProductServiceClient
	Cart      gen.CartServiceClient
	Order     gen.OrderServiceClient
	Payment   gen.PaymentServiceClient
	Inventory gen.InventoryServiceClient
}
