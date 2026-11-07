// Package repository persists users and refresh tokens in PostgreSQL.
package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Varsh1l01/VENDORA/pkg/apperr"
	"github.com/Varsh1l01/VENDORA/pkg/db"
	"github.com/Varsh1l01/VENDORA/pkg/events"
	"github.com/Varsh1l01/VENDORA/pkg/outbox"
)

// User is a stored account.
type User struct {
	ID           string
	Email        string
	PasswordHash string
	FirstName    string
	LastName     string
	Role         string
	CreatedAt    time.Time
}

// RefreshToken is a stored (hashed) refresh token.
type RefreshToken struct {
	ID        string
	UserID    string
	ExpiresAt time.Time
	RevokedAt *time.Time
}

// Postgres implements the user store.
type Postgres struct{ pool *pgxpool.Pool }

// New returns a Postgres store.
func New(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

const userCols = `id, email, password_hash, first_name, last_name, role, created_at`

func scanUser(row pgx.Row) (*User, error) {
	var u User
	if err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.FirstName, &u.LastName, &u.Role, &u.CreatedAt); err != nil {
		return nil, err
	}
	return &u, nil
}

// CreateUser inserts u and enqueues ev in the same transaction.
func (r *Postgres) CreateUser(ctx context.Context, u *User, ev events.Envelope) error {
	return db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO users (id, email, password_hash, first_name, last_name, role, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`,
			u.ID, u.Email, u.PasswordHash, u.FirstName, u.LastName, u.Role, u.CreatedAt)
		if err != nil {
			if db.IsUniqueViolation(err) {
				return apperr.Conflict("email is already registered")
			}
			return apperr.Internal(err, "insert user")
		}
		if err := outbox.Enqueue(ctx, tx, events.TopicUser, ev); err != nil {
			return apperr.Internal(err, "enqueue user.registered")
		}
		return nil
	})
}

// GetByEmail returns the user with email, or a NotFound error.
func (r *Postgres) GetByEmail(ctx context.Context, email string) (*User, error) {
	u, err := scanUser(r.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE email = $1`, email))
	return u, mapGet(err, "get user by email")
}

// GetByID returns the user with id, or a NotFound error.
func (r *Postgres) GetByID(ctx context.Context, id string) (*User, error) {
	u, err := scanUser(r.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
	return u, mapGet(err, "get user by id")
}

func mapGet(err error, op string) error {
	switch {
	case err == nil:
		return nil
	case db.IsNoRows(err):
		return apperr.NotFound("user not found")
	default:
		return apperr.Internal(err, op)
	}
}

// EnsureAdmin creates an admin account, or promotes an existing account with
// that email to admin. The password of an existing account is not changed.
func (r *Postgres) EnsureAdmin(ctx context.Context, u *User) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, first_name, last_name, role)
		VALUES ($1, $2, $3, $4, $5, 'admin')
		ON CONFLICT (email) DO UPDATE SET role = 'admin', updated_at = now()`,
		u.ID, u.Email, u.PasswordHash, u.FirstName, u.LastName)
	if err != nil {
		return fmt.Errorf("ensure admin: %w", err)
	}
	return nil
}

// InsertRefreshToken stores a hashed refresh token.
func (r *Postgres) InsertRefreshToken(ctx context.Context, id, userID string, hash []byte, expires time.Time) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at) VALUES ($1, $2, $3, $4)`,
		id, userID, hash, expires)
	if err != nil {
		return apperr.Internal(err, "insert refresh token")
	}
	return nil
}

// RotateResult describes the outcome of RotateRefreshToken.
type RotateResult int

const (
	Rotated RotateResult = iota
	TokenUnknown
	TokenExpired
	TokenReused // a revoked token was presented: likely theft
)

// RotateRefreshToken atomically revokes the token identified by oldHash and
// stores newHash in its place. If the old token was already revoked (reuse),
// every token of that user is revoked.
func (r *Postgres) RotateRefreshToken(ctx context.Context, oldHash []byte, newID string, newHash []byte, newExpires time.Time) (string, RotateResult, error) {
	var userID string
	result := Rotated
	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		var id string
		var expires time.Time
		var revoked *time.Time
		err := tx.QueryRow(ctx, `
			SELECT id, user_id, expires_at, revoked_at FROM refresh_tokens
			WHERE token_hash = $1 FOR UPDATE`, oldHash).Scan(&id, &userID, &expires, &revoked)
		if db.IsNoRows(err) {
			result = TokenUnknown
			return nil
		}
		if err != nil {
			return apperr.Internal(err, "select refresh token")
		}
		if revoked != nil {
			result = TokenReused
			_, err := tx.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, userID)
			if err != nil {
				return apperr.Internal(err, "revoke all refresh tokens")
			}
			return nil
		}
		if time.Now().After(expires) {
			result = TokenExpired
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = now(), replaced_by = $2 WHERE id = $1`, id, newID); err != nil {
			return apperr.Internal(err, "revoke refresh token")
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at) VALUES ($1, $2, $3, $4)`,
			newID, userID, newHash, newExpires); err != nil {
			return apperr.Internal(err, "insert rotated refresh token")
		}
		return nil
	})
	return userID, result, err
}

// RevokeRefreshToken revokes the token with hash (no-op if unknown).
func (r *Postgres) RevokeRefreshToken(ctx context.Context, hash []byte) error {
	_, err := r.pool.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = now() WHERE token_hash = $1 AND revoked_at IS NULL`, hash)
	if err != nil {
		return apperr.Internal(err, "revoke refresh token")
	}
	return nil
}
