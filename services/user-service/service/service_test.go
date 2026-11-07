package service

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Varsh1l01/VENDORA/pkg/apperr"
	"github.com/Varsh1l01/VENDORA/pkg/auth"
	"github.com/Varsh1l01/VENDORA/pkg/events"
	"github.com/Varsh1l01/VENDORA/services/user-service/repository"
)

type token struct {
	userID  string
	hash    []byte
	expires time.Time
	revoked bool
}

type fakeStore struct {
	mu     sync.Mutex
	users  map[string]*repository.User
	tokens []*token
	events []events.Envelope
}

func newFake() *fakeStore { return &fakeStore{users: map[string]*repository.User{}} }

func (f *fakeStore) CreateUser(_ context.Context, u *repository.User, ev events.Envelope) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, x := range f.users {
		if x.Email == u.Email {
			return apperr.Conflict("email is already registered")
		}
	}
	c := *u
	f.users[u.ID] = &c
	f.events = append(f.events, ev)
	return nil
}

func (f *fakeStore) GetByEmail(_ context.Context, email string) (*repository.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.users {
		if u.Email == email {
			c := *u
			return &c, nil
		}
	}
	return nil, apperr.NotFound("user not found")
}

func (f *fakeStore) GetByID(_ context.Context, id string) (*repository.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.users[id]; ok {
		c := *u
		return &c, nil
	}
	return nil, apperr.NotFound("user not found")
}

func (f *fakeStore) EnsureAdmin(_ context.Context, u *repository.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, x := range f.users {
		if x.Email == u.Email {
			x.Role = auth.RoleAdmin
			return nil
		}
	}
	c := *u
	c.Role = auth.RoleAdmin
	f.users[u.ID] = &c
	return nil
}

func (f *fakeStore) InsertRefreshToken(_ context.Context, _, userID string, hash []byte, exp time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens = append(f.tokens, &token{userID: userID, hash: hash, expires: exp})
	return nil
}

func (f *fakeStore) RotateRefreshToken(_ context.Context, old []byte, _ string, newHash []byte, exp time.Time) (string, repository.RotateResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.tokens {
		if !bytes.Equal(t.hash, old) {
			continue
		}
		if t.revoked {
			for _, o := range f.tokens {
				if o.userID == t.userID {
					o.revoked = true
				}
			}
			return t.userID, repository.TokenReused, nil
		}
		if time.Now().After(t.expires) {
			return t.userID, repository.TokenExpired, nil
		}
		t.revoked = true
		f.tokens = append(f.tokens, &token{userID: t.userID, hash: newHash, expires: exp})
		return t.userID, repository.Rotated, nil
	}
	return "", repository.TokenUnknown, nil
}

func (f *fakeStore) RevokeRefreshToken(_ context.Context, hash []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.tokens {
		if bytes.Equal(t.hash, hash) {
			t.revoked = true
		}
	}
	return nil
}

func newSvc() (*Service, *fakeStore, *auth.Tokens) {
	st := newFake()
	tk := auth.NewTokens("user-service-test-secret-0123456789ab", "iss", "aud")
	return New(st, tk, Config{AccessTTL: time.Minute, RefreshTTL: time.Hour, BcryptCost: bcrypt.MinCost}), st, tk
}

var valid = RegisterInput{Email: "Ada@Example.com", Password: "correct-horse", FirstName: "Ada", LastName: "Lovelace"}

func TestRegister(t *testing.T) {
	svc, st, tk := newSvc()
	u, toks, err := svc.Register(context.Background(), valid)
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "ada@example.com" || u.Role != auth.RoleCustomer {
		t.Fatalf("user = %+v", u)
	}
	if u.PasswordHash == valid.Password || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(valid.Password)) != nil {
		t.Fatal("password not hashed with bcrypt")
	}
	p, err := tk.Verify(toks.Access)
	if err != nil || p.Subject != u.ID {
		t.Fatalf("access token: %+v %v", p, err)
	}
	if len(st.events) != 1 || st.events[0].Type != events.UserRegistered {
		t.Fatalf("events = %+v", st.events)
	}
	if _, _, err := svc.Register(context.Background(), valid); !apperr.IsKind(err, apperr.KindConflict) {
		t.Fatalf("duplicate email: %v", err)
	}
}

func TestRegisterValidation(t *testing.T) {
	svc, _, _ := newSvc()
	bad := []RegisterInput{
		{Email: "nope", Password: "correct-horse", FirstName: "A", LastName: "B"},
		{Email: "a@b.co\r\nBcc: x@y.z", Password: "correct-horse", FirstName: "A", LastName: "B"},
		{Email: "a@b.co", Password: "short", FirstName: "A", LastName: "B"},
		{Email: "a@b.co", Password: string(make([]byte, 73)), FirstName: "A", LastName: "B"},
		{Email: "a@b.co", Password: "correct-horse", FirstName: " ", LastName: "B"},
	}
	for _, in := range bad {
		if _, _, err := svc.Register(context.Background(), in); !apperr.IsKind(err, apperr.KindInvalid) {
			t.Errorf("Register(%q) = %v, want Invalid", in.Email, err)
		}
	}
}

func TestLoginErrorsAreIndistinguishable(t *testing.T) {
	svc, _, _ := newSvc()
	if _, _, err := svc.Register(context.Background(), valid); err != nil {
		t.Fatal(err)
	}
	_, _, wrongPw := svc.Login(context.Background(), "ada@example.com", "wrong-password")
	_, _, noUser := svc.Login(context.Background(), "ghost@example.com", "wrong-password")
	if wrongPw == nil || noUser == nil || wrongPw.Error() != noUser.Error() {
		t.Fatalf("errors differ: %v vs %v", wrongPw, noUser)
	}
	if _, _, err := svc.Login(context.Background(), "ADA@example.com", valid.Password); err != nil {
		t.Fatalf("case-insensitive login: %v", err)
	}
}

func TestRefreshRotationAndReuse(t *testing.T) {
	svc, _, _ := newSvc()
	_, t1, err := svc.Register(context.Background(), valid)
	if err != nil {
		t.Fatal(err)
	}
	t2, err := svc.Refresh(context.Background(), t1.Refresh)
	if err != nil || t2.Refresh == t1.Refresh {
		t.Fatalf("rotation: %v", err)
	}
	if _, err := svc.Refresh(context.Background(), t1.Refresh); !apperr.IsKind(err, apperr.KindUnauthenticated) {
		t.Fatalf("reuse accepted: %v", err)
	}
	// Reuse revoked the whole family, including the newest token.
	if _, err := svc.Refresh(context.Background(), t2.Refresh); !apperr.IsKind(err, apperr.KindUnauthenticated) {
		t.Fatalf("token family not revoked: %v", err)
	}
}

func TestLogoutRevokes(t *testing.T) {
	svc, _, _ := newSvc()
	_, toks, _ := svc.Register(context.Background(), valid)
	if err := svc.Logout(context.Background(), toks.Refresh); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Refresh(context.Background(), toks.Refresh); err == nil {
		t.Fatal("refresh after logout succeeded")
	}
}

func TestGetUserOwnership(t *testing.T) {
	svc, _, _ := newSvc()
	u, _, _ := svc.Register(context.Background(), valid)
	other := auth.WithPrincipal(context.Background(), auth.Principal{Subject: "22222222-2222-4222-8222-222222222222", Role: auth.RoleCustomer})
	if _, err := svc.GetUser(other, u.ID); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("other user read profile: %v", err)
	}
	svcCtx := auth.WithPrincipal(context.Background(), auth.Principal{Subject: "notification-service", Role: auth.RoleService})
	if _, err := svc.GetUser(svcCtx, u.ID); err != nil {
		t.Fatalf("service read: %v", err)
	}
}

func TestEnsureAdminPromotes(t *testing.T) {
	svc, st, _ := newSvc()
	u, _, _ := svc.Register(context.Background(), valid)
	if err := svc.EnsureAdmin(context.Background(), valid.Email, "another-password"); err != nil {
		t.Fatal(err)
	}
	if st.users[u.ID].Role != auth.RoleAdmin {
		t.Fatal("existing account not promoted")
	}
}
