package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type User struct {
	ID        string
	Email     string
	Password  string // hashed
	FirstName string
	LastName  string
	Role      string
	CreatedAt time.Time
}

type UserRepository struct {
	pool *pgxpool.Pool
}

func NewUserRepository(dbURL string) (*UserRepository, error) {
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		return nil, err
	}
	return &UserRepository{pool: pool}, nil
}

func (r *UserRepository) Create(ctx context.Context, user *User) error {
	user.ID = uuid.New().String()
	user.CreatedAt = time.Now()

	_, err := r.pool.Exec(ctx, `
		INSERT INTO users (id, email, password, first_name, last_name, role, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, user.ID, user.Email, user.Password, user.FirstName, user.LastName, user.Role, user.CreatedAt)

	return err
}

func (r *UserRepository) GetByEmail(ctx context.Context, email string) (*User, error) {
	var user User
	err := r.pool.QueryRow(ctx, `
		SELECT id, email, password, first_name, last_name, role, created_at
		FROM users WHERE email = $1
	`, email).Scan(&user.ID, &user.Email, &user.Password, &user.FirstName, &user.LastName, &user.Role, &user.CreatedAt)

	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *UserRepository) GetByID(ctx context.Context, id string) (*User, error) {
	var user User
	err := r.pool.QueryRow(ctx, `
		SELECT id, email, password, first_name, last_name, role, created_at
		FROM users WHERE id = $1
	`, id).Scan(&user.ID, &user.Email, &user.Password, &user.FirstName, &user.LastName, &user.Role, &user.CreatedAt)

	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *UserRepository) Close() {
	r.pool.Close()
}
