package grpcx

import (
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/Varsh1l01/VENDORA/pkg/auth"
	"github.com/Varsh1l01/VENDORA/pkg/requestid"
)

// DefaultCallTimeout bounds calls whose context has no deadline.
const DefaultCallTimeout = 3 * time.Second

// serviceConfig enables client-side round-robin across all resolved backend
// addresses and retries UNAVAILABLE for idempotent methods only.
const serviceConfig = `{
  "loadBalancingConfig": [{"round_robin": {}}],
  "methodConfig": [{
    "name": [
      {"service": "vendora.product.v1.ProductService", "method": "GetProduct"},
      {"service": "vendora.product.v1.ProductService", "method": "BatchGetProducts"},
      {"service": "vendora.product.v1.ProductService", "method": "ListProducts"},
      {"service": "vendora.cart.v1.CartService", "method": "GetCart"},
      {"service": "vendora.cart.v1.CartService", "method": "ClearCart"},
      {"service": "vendora.inventory.v1.InventoryService", "method": "GetStock"},
      {"service": "vendora.inventory.v1.InventoryService", "method": "ReserveStock"},
      {"service": "vendora.inventory.v1.InventoryService", "method": "ReleaseStock"},
      {"service": "vendora.order.v1.OrderService", "method": "GetOrder"},
      {"service": "vendora.order.v1.OrderService", "method": "ListOrders"},
      {"service": "vendora.order.v1.OrderService", "method": "CreateOrder"},
      {"service": "vendora.user.v1.UserService", "method": "GetUser"},
      {"service": "vendora.payment.v1.PaymentService", "method": "GetPaymentByOrder"}
    ],
    "retryPolicy": {
      "maxAttempts": 3,
      "initialBackoff": "0.1s",
      "maxBackoff": "1s",
      "backoffMultiplier": 2.0,
      "retryableStatusCodes": ["UNAVAILABLE"]
    }
  }]
}`

// ClientOptions configures Dial.
type ClientOptions struct {
	// ServiceIdentity, when set, authenticates calls as this service unless
	// the context already carries a forwarded bearer token (WithBearer).
	ServiceIdentity *ServiceIdentity
	Timeout         time.Duration
}

// Dial creates a lazily-connecting client connection. The "dns:///" scheme is
// used so that round_robin balances across all A records of target.
func Dial(target string, o ClientOptions) (*grpc.ClientConn, error) {
	if o.Timeout == 0 {
		o.Timeout = DefaultCallTimeout
	}
	conn, err := grpc.NewClient("dns:///"+target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(serviceConfig),
		grpc.WithChainUnaryInterceptor(clientInterceptor(o)),
	)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", target, err)
	}
	return conn, nil
}

type bearerKey struct{}

// WithBearer makes outgoing calls on ctx forward token (e.g. the end user's
// access token from the gateway) instead of the service identity.
func WithBearer(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, bearerKey{}, token)
}

func clientInterceptor(o ClientOptions) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if _, ok := ctx.Deadline(); !ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, o.Timeout)
			defer cancel()
		}
		if id := requestid.From(ctx); id != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, requestid.MetadataKey, id)
		}
		if tok, ok := ctx.Value(bearerKey{}).(string); ok && tok != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+tok)
		} else if o.ServiceIdentity != nil {
			tok, err := o.ServiceIdentity.Token()
			if err != nil {
				return err
			}
			ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+tok)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// ServiceIdentity mints and caches short-lived service tokens.
type ServiceIdentity struct {
	tokens *auth.Tokens
	name   string

	mu  sync.Mutex
	tok string
	exp time.Time
}

// NewServiceIdentity returns an identity for service name.
func NewServiceIdentity(tokens *auth.Tokens, name string) *ServiceIdentity {
	return &ServiceIdentity{tokens: tokens, name: name}
}

// Token returns a valid service token, renewing it before expiry.
func (s *ServiceIdentity) Token() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tok != "" && time.Until(s.exp) > time.Minute {
		return s.tok, nil
	}
	tok, exp, err := s.tokens.Issue(auth.Principal{Subject: s.name, Role: auth.RoleService}, 5*time.Minute)
	if err != nil {
		return "", err
	}
	s.tok, s.exp = tok, exp
	return tok, nil
}
