// Package handler adapts the user service to gRPC.
package handler

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	userv1 "github.com/Varsh1l01/VENDORA/gen/vendora/user/v1"
	"github.com/Varsh1l01/VENDORA/pkg/auth"
	"github.com/Varsh1l01/VENDORA/pkg/grpcx"
	"github.com/Varsh1l01/VENDORA/services/user-service/repository"
	"github.com/Varsh1l01/VENDORA/services/user-service/service"
)

// Policy is the per-method authorization policy.
var Policy = grpcx.Policy{
	userv1.UserService_Register_FullMethodName: grpcx.Public(),
	userv1.UserService_Login_FullMethodName:    grpcx.Public(),
	userv1.UserService_Refresh_FullMethodName:  grpcx.Public(),
	userv1.UserService_Logout_FullMethodName:   grpcx.Public(),
	userv1.UserService_GetUser_FullMethodName:  grpcx.Allow(auth.RoleCustomer, auth.RoleAdmin, auth.RoleService),
}

// Handler implements userv1.UserServiceServer.
type Handler struct {
	userv1.UnimplementedUserServiceServer
	svc *service.Service
}

// New returns a Handler.
func New(svc *service.Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) Register(ctx context.Context, req *userv1.RegisterRequest) (*userv1.RegisterResponse, error) {
	u, t, err := h.svc.Register(ctx, service.RegisterInput{
		Email: req.GetEmail(), Password: req.GetPassword(), FirstName: req.GetFirstName(), LastName: req.GetLastName(),
	})
	if err != nil {
		return nil, err
	}
	return &userv1.RegisterResponse{User: toProto(u), Tokens: tokens(t)}, nil
}

func (h *Handler) Login(ctx context.Context, req *userv1.LoginRequest) (*userv1.LoginResponse, error) {
	u, t, err := h.svc.Login(ctx, req.GetEmail(), req.GetPassword())
	if err != nil {
		return nil, err
	}
	return &userv1.LoginResponse{User: toProto(u), Tokens: tokens(t)}, nil
}

func (h *Handler) Refresh(ctx context.Context, req *userv1.RefreshRequest) (*userv1.RefreshResponse, error) {
	t, err := h.svc.Refresh(ctx, req.GetRefreshToken())
	if err != nil {
		return nil, err
	}
	return &userv1.RefreshResponse{Tokens: tokens(t)}, nil
}

func (h *Handler) Logout(ctx context.Context, req *userv1.LogoutRequest) (*userv1.LogoutResponse, error) {
	if err := h.svc.Logout(ctx, req.GetRefreshToken()); err != nil {
		return nil, err
	}
	return &userv1.LogoutResponse{}, nil
}

func (h *Handler) GetUser(ctx context.Context, req *userv1.GetUserRequest) (*userv1.GetUserResponse, error) {
	u, err := h.svc.GetUser(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return &userv1.GetUserResponse{User: toProto(u)}, nil
}

func toProto(u *repository.User) *userv1.User {
	return &userv1.User{
		Id:        u.ID,
		Email:     u.Email,
		FirstName: u.FirstName,
		LastName:  u.LastName,
		Role:      u.Role,
		CreatedAt: timestamppb.New(u.CreatedAt),
	}
}

func tokens(t service.Tokens) *userv1.TokenPair {
	return &userv1.TokenPair{
		AccessToken:           t.Access,
		AccessTokenExpiresAt:  timestamppb.New(t.AccessExpires),
		RefreshToken:          t.Refresh,
		RefreshTokenExpiresAt: timestamppb.New(t.RefreshExpires),
	}
}
