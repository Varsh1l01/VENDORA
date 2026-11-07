package grpcx

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	productv1 "github.com/Varsh1l01/VENDORA/gen/vendora/product/v1"
	"github.com/Varsh1l01/VENDORA/pkg/apperr"
	"github.com/Varsh1l01/VENDORA/pkg/auth"
)

type productStub struct {
	productv1.UnimplementedProductServiceServer
	sawRole string
}

func (p *productStub) GetProduct(ctx context.Context, _ *productv1.GetProductRequest) (*productv1.GetProductResponse, error) {
	if pr, ok := auth.FromContext(ctx); ok {
		p.sawRole = pr.Role
	}
	return &productv1.GetProductResponse{}, nil
}

func (p *productStub) CreateProduct(context.Context, *productv1.CreateProductRequest) (*productv1.CreateProductResponse, error) {
	return nil, apperr.Internal(context.DeadlineExceeded, "db password=hunter2")
}

func (p *productStub) ListProducts(context.Context, *productv1.ListProductsRequest) (*productv1.ListProductsResponse, error) {
	panic("boom")
}

func setup(t *testing.T) (productv1.ProductServiceClient, *auth.Tokens, *productStub, *grpc.ClientConn) {
	t.Helper()
	tokens := auth.NewTokens("grpcx-test-secret-0123456789abcdefgh", "iss", "aud")
	srv := NewServer(ServerOptions{Tokens: tokens, Policy: Policy{
		productv1.ProductService_GetProduct_FullMethodName:    Public(),
		productv1.ProductService_CreateProduct_FullMethodName: Allow(auth.RoleAdmin),
		productv1.ProductService_ListProducts_FullMethodName:  Public(),
		// UpdateProduct intentionally missing: must be denied by default.
	}})
	stub := &productStub{}
	productv1.RegisterProductServiceServer(srv.Server, stub)
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///buf",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(clientInterceptor(ClientOptions{Timeout: time.Second})))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return productv1.NewProductServiceClient(conn), tokens, stub, conn
}

func TestPolicyEnforcement(t *testing.T) {
	c, tokens, stub, _ := setup(t)
	ctx := context.Background()

	if _, err := c.GetProduct(ctx, &productv1.GetProductRequest{}); err != nil {
		t.Fatalf("public method: %v", err)
	}
	if _, err := c.UpdateProduct(ctx, &productv1.UpdateProductRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("unlisted method must be denied, got %v", err)
	}
	if _, err := c.CreateProduct(ctx, &productv1.CreateProductRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anonymous admin call: %v", err)
	}
	cust, _, _ := tokens.Issue(auth.Principal{Subject: "u", Role: auth.RoleCustomer}, time.Minute)
	if _, err := c.CreateProduct(WithBearer(ctx, cust), &productv1.CreateProductRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("customer admin call: %v", err)
	}

	// Service identity is attached when no bearer is forwarded.
	id := NewServiceIdentity(tokens, "cart-service")
	tok, err := id.Token()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetProduct(WithBearer(ctx, tok), &productv1.GetProductRequest{}); err != nil || stub.sawRole != auth.RoleService {
		t.Fatalf("service principal not propagated: role=%q err=%v", stub.sawRole, err)
	}
}

func TestInternalErrorsAndPanicsAreSanitized(t *testing.T) {
	c, tokens, _, _ := setup(t)
	adm, _, _ := tokens.Issue(auth.Principal{Subject: "a", Role: auth.RoleAdmin}, time.Minute)
	_, err := c.CreateProduct(WithBearer(context.Background(), adm), &productv1.CreateProductRequest{})
	if st := status.Convert(err); st.Code() != codes.Internal || st.Message() != "internal error" {
		t.Fatalf("internal error leaked: %v", st)
	}
	_, err = c.ListProducts(context.Background(), &productv1.ListProductsRequest{})
	if status.Code(err) != codes.Internal {
		t.Fatalf("panic not recovered: %v", err)
	}
}

func TestHealthIsUnauthenticated(t *testing.T) {
	_, _, _, conn := setup(t)
	resp, err := healthpb.NewHealthClient(conn).Check(context.Background(), &healthpb.HealthCheckRequest{})
	if err != nil || resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("health: %v %v", resp, err)
	}
}
