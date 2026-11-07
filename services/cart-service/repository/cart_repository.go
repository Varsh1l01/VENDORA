package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
)

type CartItem struct {
	ProductID string  `json:"product_id"`
	Name      string  `json:"name"`
	Price     float64 `json:"price"`
	Quantity  int32   `json:"quantity"`
	ImageURL  string  `json:"image_url"`
}

type Cart struct {
	UserID string
	Items  []CartItem
	Total  float64
}

type CartRepository struct {
	client *redis.Client
}

func NewCartRepository(redisURL string) (*CartRepository, error) {
	client := redis.NewClient(&redis.Options{
		Addr: redisURL,
	})

	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, err
	}

	return &CartRepository{client: client}, nil
}

func (r *CartRepository) key(userID string) string {
	return fmt.Sprintf("cart:%s", userID)
}

func (r *CartRepository) GetCart(ctx context.Context, userID string) (*Cart, error) {
	data, err := r.client.Get(ctx, r.key(userID)).Bytes()
	if err == redis.Nil {
		return &Cart{UserID: userID, Items: []CartItem{}}, nil
	}
	if err != nil {
		return nil, err
	}

	var items []CartItem
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	cart := &Cart{UserID: userID, Items: items}
	cart.calculateTotal()
	return cart, nil
}

func (r *CartRepository) SaveCart(ctx context.Context, cart *Cart) error {
	data, _ := json.Marshal(cart.Items)
	return r.client.Set(ctx, r.key(cart.UserID), data, 0).Err()
}

func (r *CartRepository) ClearCart(ctx context.Context, userID string) error {
	return r.client.Del(ctx, r.key(userID)).Err()
}

func (c *Cart) calculateTotal() {
	c.Total = 0
	for _, item := range c.Items {
		c.Total += item.Price * float64(item.Quantity)
	}
}

func (r *CartRepository) Close() error {
	return r.client.Close()
}
