// Package service implements user-service business logic: registration,
// authentication and refresh-token rotation.
package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/Varsh1l01/VENDORA/pkg/apperr"
	"github.com/Varsh1l01/VENDORA/pkg/auth"
	"github.com/Varsh1l01/VENDORA/pkg/events"
	"github.com/Varsh1l01/VENDORA/pkg/logx"
	"github.com/Varsh1l01/VENDORA/pkg/requestid"
	"github.com/Varsh1l01/VENDORA/pkg/validate"
	"github.com/Varsh1l01/VENDORA/services/user-service/repository"
)

// Store is the persistence port used by the service.
type Store interface {
	CreateUser(ctx context.Context, u *repository.User, ev events.Envelope) error
	GetByEmail(ctx context.Context, email string) (*repository.User, error)
	GetByID(ctx context.Context, id string) (*repository.User, error)
	EnsureAdmin(ctx context.Context, u *repository.User) error
	InsertRefreshToken(ctx context.Context, id, userID string, hash []byte, expires time.Time) error
	RotateRefreshToken(ctx context.Context, oldHash []byte, newID string, newHash []byte, newExpires time.Time) (string, repository.RotateResult, error)
	RevokeRefreshToken(ctx context.Context, hash []byte) error
}

// Config holds token lifetimes and hashing cost.
type Config struct {
	AccessTTL  time.Duration
	RefreshTTL time.Duration
	BcryptCost int
}

// Tokens is an issued token pair.
type Tokens struct {
	Access         string
	AccessExpires  time.Time
	Refresh        string
	RefreshExpires time.Time
}

// Service is the user domain service.
type Service struct {
	store     Store
	tokens    *auth.Tokens
	cfg       Config
	dummyHash []byte
}

// New returns a Service.
func New(store Store, tokens *auth.Tokens, cfg Config) *Service {
	if cfg.BcryptCost == 0 {
		cfg.BcryptCost = bcrypt.DefaultCost
	}
	// Used to spend the same time on unknown emails as on wrong passwords,
	// so login timing does not reveal which emails are registered.
	dummy, _ := bcrypt.GenerateFromPassword([]byte("vendora-timing-equalizer"), cfg.BcryptCost)
	return &Service{store: store, tokens: tokens, cfg: cfg, dummyHash: dummy}
}

// RegisterInput is the registration request.
type RegisterInput struct {
	Email, Password, FirstName, LastName string
}

// Register creates a customer account and returns it with a token pair.
func (s *Service) Register(ctx context.Context, in RegisterInput) (*repository.User, Tokens, error) {
	email, err := validate.Email(in.Email)
	if err != nil {
		return nil, Tokens{}, err
	}
	if err := validate.Password(in.Password); err != nil {
		return nil, Tokens{}, err
	}
	first, err := validate.Text("first_name", in.FirstName, 1, 100)
	if err != nil {
		return nil, Tokens{}, err
	}
	last, err := validate.Text("last_name", in.LastName, 1, 100)
	if err != nil {
		return nil, Tokens{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), s.cfg.BcryptCost)
	if err != nil {
		return nil, Tokens{}, apperr.Internal(err, "hash password")
	}
	u := &repository.User{
		ID:           uuid.NewString(),
		Email:        email,
		PasswordHash: string(hash),
		FirstName:    first,
		LastName:     last,
		Role:         auth.RoleCustomer,
		CreatedAt:    time.Now().UTC(),
	}
	ev, err := events.New(events.UserRegistered, u.ID, requestid.From(ctx), events.UserRegisteredData{UserID: u.ID})
	if err != nil {
		return nil, Tokens{}, apperr.Internal(err, "build event")
	}
	if err := s.store.CreateUser(ctx, u, ev); err != nil {
		return nil, Tokens{}, err
	}
	toks, err := s.issue(ctx, u)
	if err != nil {
		return nil, Tokens{}, err
	}
	logx.From(ctx).Info("user registered", "user_id", u.ID, "email", logx.RedactEmail(email))
	return u, toks, nil
}

var errBadCredentials = apperr.Unauthenticated("invalid email or password")

// Login verifies credentials and returns the user with a token pair.
func (s *Service) Login(ctx context.Context, email, password string) (*repository.User, Tokens, error) {
	email, err := validate.Email(email)
	if err != nil || len(password) == 0 || len(password) > validate.MaxPasswordLen {
		_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(password))
		return nil, Tokens{}, errBadCredentials
	}
	u, err := s.store.GetByEmail(ctx, email)
	if err != nil {
		if apperr.IsKind(err, apperr.KindNotFound) {
			_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(password))
			return nil, Tokens{}, errBadCredentials
		}
		return nil, Tokens{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return nil, Tokens{}, errBadCredentials
	}
	toks, err := s.issue(ctx, u)
	if err != nil {
		return nil, Tokens{}, err
	}
	return u, toks, nil
}

// Refresh rotates a refresh token and returns a new pair.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	if refreshToken == "" || len(refreshToken) > 256 {
		return Tokens{}, apperr.Unauthenticated("invalid refresh token")
	}
	newRaw, newHash, err := newRefreshToken()
	if err != nil {
		return Tokens{}, apperr.Internal(err, "generate refresh token")
	}
	newExp := time.Now().Add(s.cfg.RefreshTTL)
	userID, res, err := s.store.RotateRefreshToken(ctx, hashToken(refreshToken), uuid.NewString(), newHash, newExp)
	if err != nil {
		return Tokens{}, err
	}
	switch res {
	case repository.TokenReused:
		logx.From(ctx).Warn("refresh token reuse detected; all sessions revoked", "user_id", userID)
		return Tokens{}, apperr.Unauthenticated("invalid refresh token")
	case repository.TokenUnknown, repository.TokenExpired:
		return Tokens{}, apperr.Unauthenticated("invalid refresh token")
	}
	u, err := s.store.GetByID(ctx, userID)
	if err != nil {
		return Tokens{}, err
	}
	access, accessExp, err := s.tokens.Issue(principal(u), s.cfg.AccessTTL)
	if err != nil {
		return Tokens{}, apperr.Internal(err, "issue access token")
	}
	return Tokens{Access: access, AccessExpires: accessExp, Refresh: newRaw, RefreshExpires: newExp}, nil
}

// Logout revokes a refresh token. Unknown tokens are ignored.
func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	if refreshToken == "" || len(refreshToken) > 256 {
		return nil
	}
	return s.store.RevokeRefreshToken(ctx, hashToken(refreshToken))
}

// GetUser returns a user visible to the caller.
func (s *Service) GetUser(ctx context.Context, id string) (*repository.User, error) {
	if err := validate.ID("id", id); err != nil {
		return nil, err
	}
	if !auth.CanAccessOwnedBy(ctx, id) {
		return nil, apperr.NotFound("user not found")
	}
	return s.store.GetByID(ctx, id)
}

// EnsureAdmin bootstraps an administrator account.
func (s *Service) EnsureAdmin(ctx context.Context, email, password string) error {
	email, err := validate.Email(email)
	if err != nil {
		return err
	}
	if err := validate.Password(password); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.cfg.BcryptCost)
	if err != nil {
		return err
	}
	return s.store.EnsureAdmin(ctx, &repository.User{
		ID: uuid.NewString(), Email: email, PasswordHash: string(hash), FirstName: "Admin", LastName: "User",
	})
}

func (s *Service) issue(ctx context.Context, u *repository.User) (Tokens, error) {
	access, accessExp, err := s.tokens.Issue(principal(u), s.cfg.AccessTTL)
	if err != nil {
		return Tokens{}, apperr.Internal(err, "issue access token")
	}
	raw, hash, err := newRefreshToken()
	if err != nil {
		return Tokens{}, apperr.Internal(err, "generate refresh token")
	}
	refreshExp := time.Now().Add(s.cfg.RefreshTTL)
	if err := s.store.InsertRefreshToken(ctx, uuid.NewString(), u.ID, hash, refreshExp); err != nil {
		return Tokens{}, err
	}
	return Tokens{Access: access, AccessExpires: accessExp, Refresh: raw, RefreshExpires: refreshExp}, nil
}

func principal(u *repository.User) auth.Principal {
	return auth.Principal{Subject: u.ID, Email: u.Email, Role: u.Role}
}

func newRefreshToken() (string, []byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	raw := base64.RawURLEncoding.EncodeToString(b)
	return raw, hashToken(raw), nil
}

// hashToken stores only a SHA-256 of refresh tokens: they are high-entropy
// random values, so a fast hash is appropriate (unlike passwords).
func hashToken(raw string) []byte {
	h := sha256.Sum256([]byte(raw))
	return h[:]
}
