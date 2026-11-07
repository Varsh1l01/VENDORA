// Package grpcx provides VENDORA's gRPC server and client plumbing:
// recovery, structured logging, metrics, authentication, a default-deny
// authorization policy, error mapping, deadlines and health checking.
package grpcx

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/Varsh1l01/VENDORA/pkg/apperr"
	"github.com/Varsh1l01/VENDORA/pkg/auth"
	"github.com/Varsh1l01/VENDORA/pkg/logx"
	"github.com/Varsh1l01/VENDORA/pkg/requestid"
)

// Rule describes who may call a method.
type Rule struct {
	Public bool     // no authentication required
	Roles  []string // allowed roles when not public
}

// Public allows unauthenticated callers.
func Public() Rule { return Rule{Public: true} }

// Allow permits the listed roles.
func Allow(roles ...string) Rule { return Rule{Roles: roles} }

// Policy maps full method names ("/pkg.Service/Method") to rules. Methods
// without a rule are denied (default deny).
type Policy map[string]Rule

var serverHandled = prometheus.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "grpc_server_handling_seconds",
	Help:    "Latency of gRPC server calls.",
	Buckets: []float64{.001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
}, []string{"method", "code"})

func init() { prometheus.MustRegister(serverHandled) }

// ServerOptions configures NewServer.
type ServerOptions struct {
	Tokens *auth.Tokens
	Policy Policy
}

// Server bundles a grpc.Server with its health service.
type Server struct {
	*grpc.Server
	Health *health.Server
}

// NewServer returns a gRPC server with VENDORA's interceptor chain and a
// registered grpc.health.v1 service.
func NewServer(o ServerOptions) *Server {
	s := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			contextInterceptor(),
			recoveryInterceptor(),
			observeInterceptor(),
			authInterceptor(o.Tokens, o.Policy),
		),
		grpc.KeepaliveParams(keepalive.ServerParameters{MaxConnectionIdle: 5 * time.Minute, Time: 1 * time.Minute}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 20 * time.Second, PermitWithoutStream: true}),
		grpc.MaxRecvMsgSize(4<<20),
		grpc.MaxConcurrentStreams(1000),
	)
	h := health.NewServer()
	healthpb.RegisterHealthServer(s, h)
	return &Server{Server: s, Health: h}
}

// contextInterceptor attaches request id and a request-scoped logger.
func contextInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		id := ""
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if v := md.Get(requestid.MetadataKey); len(v) > 0 {
				id = v[0]
			}
		}
		id = requestid.Sanitize(id)
		ctx = requestid.With(ctx, id)
		ctx = logx.With(ctx, "request_id", id, "rpc", info.FullMethod)
		return handler(ctx, req)
	}
}

func recoveryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				logx.From(ctx).Error("panic in handler", "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
				err = status.Error(codes.Internal, "internal error")
			}
		}()
		return handler(ctx, req)
	}
}

// observeInterceptor maps errors to safe gRPC statuses, logs and records metrics.
func observeInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		dur := time.Since(start)
		code := codes.OK
		if err != nil {
			orig := err
			err = apperr.ToStatus(err)
			code = status.Code(err)
			l := logx.From(ctx)
			if code == codes.Internal || code == codes.Unavailable || code == codes.Unknown {
				// Log the full cause; the client only sees the safe message.
				l.Error("rpc failed", "code", code.String(), "duration_ms", dur.Milliseconds(), "error", orig.Error())
			} else {
				l.Info("rpc rejected", "code", code.String(), "duration_ms", dur.Milliseconds(), "reason", status.Convert(err).Message())
			}
		} else if !strings.HasPrefix(info.FullMethod, "/grpc.health") {
			logx.From(ctx).Debug("rpc ok", "duration_ms", dur.Milliseconds())
		}
		serverHandled.WithLabelValues(info.FullMethod, code.String()).Observe(dur.Seconds())
		return resp, err
	}
}

func authInterceptor(tokens *auth.Tokens, policy Policy) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if strings.HasPrefix(info.FullMethod, "/grpc.health.v1.Health/") {
			return handler(ctx, req)
		}
		rule, ok := policy[info.FullMethod]
		if !ok {
			return nil, status.Error(codes.PermissionDenied, "method not allowed")
		}
		p, authErr := principalFromMetadata(ctx, tokens)
		if authErr == nil {
			ctx = auth.WithPrincipal(ctx, p)
			ctx = logx.With(ctx, "caller", p.Subject, "caller_role", p.Role)
		}
		if rule.Public {
			return handler(ctx, req)
		}
		if authErr != nil {
			return nil, apperr.ToStatus(authErr)
		}
		for _, r := range rule.Roles {
			if r == p.Role {
				return handler(ctx, req)
			}
		}
		return nil, status.Error(codes.PermissionDenied, "insufficient permissions")
	}
}

func principalFromMetadata(ctx context.Context, tokens *auth.Tokens) (auth.Principal, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return auth.Principal{}, apperr.Unauthenticated("authentication required")
	}
	vals := md.Get("authorization")
	if len(vals) == 0 {
		return auth.Principal{}, apperr.Unauthenticated("authentication required")
	}
	tok, found := strings.CutPrefix(vals[0], "Bearer ")
	if !found || tok == "" {
		return auth.Principal{}, apperr.Unauthenticated("bearer token required")
	}
	return tokens.Verify(tok)
}
