package handler

import (
	"context"

	"github.com/ayushdevani/forgecart/gen"
	"github.com/ayushdevani/forgecart/services/user-service/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type UserHandler struct {
	gen.UnimplementedUserServiceServer
	svc *service.UserService
}

func NewUserHandler(svc *service.UserService) *UserHandler {
	return &UserHandler{svc: svc}
}

func (h *UserHandler) Register(ctx context.Context, req *gen.RegisterRequest) (*gen.RegisterResponse, error) {
	user, token, err := h.svc.Register(ctx, req.Email, req.Password, req.FirstName, req.LastName)
	if err != nil {
		if err == service.ErrUserExists {
			return nil, status.Error(codes.AlreadyExists, "user already exists")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &gen.RegisterResponse{
		User: &gen.User{
			Id:        user.ID,
			Email:     user.Email,
			FirstName: user.FirstName,
			LastName:  user.LastName,
			Role:      user.Role,
			CreatedAt: user.CreatedAt.Format("2006-01-02T15:04:05Z"),
		},
		Token: token,
	}, nil
}

func (h *UserHandler) Login(ctx context.Context, req *gen.LoginRequest) (*gen.LoginResponse, error) {
	user, token, err := h.svc.Login(ctx, req.Email, req.Password)
	if err != nil {
		if err == service.ErrInvalidCredentials {
			return nil, status.Error(codes.Unauthenticated, "invalid credentials")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &gen.LoginResponse{
		User: &gen.User{
			Id:        user.ID,
			Email:     user.Email,
			FirstName: user.FirstName,
			LastName:  user.LastName,
			Role:      user.Role,
			CreatedAt: user.CreatedAt.Format("2006-01-02T15:04:05Z"),
		},
		Token: token,
	}, nil
}

func (h *UserHandler) ValidateToken(ctx context.Context, req *gen.ValidateTokenRequest) (*gen.ValidateTokenResponse, error) {
	userID, role, err := h.svc.ValidateToken(req.Token)
	if err != nil {
		return &gen.ValidateTokenResponse{Valid: false}, nil
	}

	return &gen.ValidateTokenResponse{
		Valid:  true,
		UserId: userID,
		Role:   role,
	}, nil
}

func (h *UserHandler) GetUser(ctx context.Context, req *gen.GetUserRequest) (*gen.User, error) {
	user, err := h.svc.GetUser(ctx, req.Id)
	if err != nil {
		return nil, status.Error(codes.NotFound, "user not found")
	}

	return &gen.User{
		Id:        user.ID,
		Email:     user.Email,
		FirstName: user.FirstName,
		LastName:  user.LastName,
		Role:      user.Role,
		CreatedAt: user.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}, nil
}
