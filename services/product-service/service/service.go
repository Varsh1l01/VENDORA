// Package service implements catalog business rules and the read cache.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"

	"github.com/Varsh1l01/VENDORA/pkg/apperr"
	"github.com/Varsh1l01/VENDORA/pkg/auth"
	"github.com/Varsh1l01/VENDORA/pkg/events"
	"github.com/Varsh1l01/VENDORA/pkg/logx"
	"github.com/Varsh1l01/VENDORA/pkg/pagination"
	"github.com/Varsh1l01/VENDORA/pkg/requestid"
	"github.com/Varsh1l01/VENDORA/pkg/validate"
	"github.com/Varsh1l01/VENDORA/services/product-service/repository"
)

// Store is the persistence port.
type Store interface {
	Create(ctx context.Context, p *repository.Product, ev events.Envelope) error
	Get(ctx context.Context, id string) (*repository.Product, error)
	BatchGet(ctx context.Context, ids []string) ([]*repository.Product, error)
	ListActive(ctx context.Context, limit int32, after *pagination.Cursor) ([]*repository.Product, error)
	Update(ctx context.Context, id string, p repository.Patch) (*repository.Product, error)
	Deactivate(ctx context.Context, id string) error
}

var cacheOps = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "product_cache_requests_total", Help: "Product cache lookups by result.",
}, []string{"result"})

func init() { prometheus.MustRegister(cacheOps) }

// Service is the catalog service.
type Service struct {
	store Store
	cache *redis.Client // optional
	ttl   time.Duration
	group singleflight.Group
}

// New returns a Service. cache may be nil to disable caching.
func New(store Store, cache *redis.Client, ttl time.Duration) *Service {
	return &Service{store: store, cache: cache, ttl: ttl}
}

// CreateInput is a new product.
type CreateInput struct {
	Name, Description, ImageURL string
	PriceCents                  int64
	InitialStock                int32
}

// Create validates and stores a product, and emits product.created so that
// inventory-service provisions its stock row.
func (s *Service) Create(ctx context.Context, in CreateInput) (*repository.Product, error) {
	name, err := validate.Text("name", in.Name, 1, 200)
	if err != nil {
		return nil, err
	}
	desc, err := validate.Text("description", in.Description, 0, 5000)
	if err != nil {
		return nil, err
	}
	img, err := validate.URL("image_url", in.ImageURL)
	if err != nil {
		return nil, err
	}
	if err := validate.PriceCents(in.PriceCents); err != nil {
		return nil, err
	}
	if err := validate.Stock(in.InitialStock); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	p := &repository.Product{
		ID: uuid.NewString(), Name: name, Description: desc, PriceCents: in.PriceCents,
		Currency: "USD", ImageURL: img, IsActive: true, CreatedAt: now, UpdatedAt: now,
	}
	ev, err := events.New(events.ProductCreated, p.ID, requestid.From(ctx),
		events.ProductCreatedData{ProductID: p.ID, InitialStock: in.InitialStock})
	if err != nil {
		return nil, apperr.Internal(err, "build event")
	}
	if err := s.store.Create(ctx, p, ev); err != nil {
		return nil, err
	}
	return p, nil
}

// Get returns a product. Inactive products are hidden from customers and
// anonymous callers.
func (s *Service) Get(ctx context.Context, id string) (*repository.Product, error) {
	if err := validate.ID("id", id); err != nil {
		return nil, err
	}
	p, err := s.getCached(ctx, id)
	if err != nil {
		return nil, err
	}
	if !p.IsActive && !privileged(ctx) {
		return nil, apperr.NotFound("product not found")
	}
	return p, nil
}

// BatchGet returns the existing products among ids (max 100), including
// inactive ones for privileged callers so they can detect deactivation.
func (s *Service) BatchGet(ctx context.Context, ids []string) ([]*repository.Product, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > validate.MaxPageSize {
		return nil, apperr.Invalid("at most %d ids per request", validate.MaxPageSize)
	}
	for _, id := range ids {
		if err := validate.ID("ids", id); err != nil {
			return nil, err
		}
	}
	found, missing := s.cacheMGet(ctx, ids)
	if len(missing) > 0 {
		fromDB, err := s.store.BatchGet(ctx, missing)
		if err != nil {
			return nil, err
		}
		for _, p := range fromDB {
			s.cacheSet(ctx, p)
		}
		found = append(found, fromDB...)
	}
	if privileged(ctx) {
		return found, nil
	}
	out := found[:0]
	for _, p := range found {
		if p.IsActive {
			out = append(out, p)
		}
	}
	return out, nil
}

// List returns a page of active products and the next page token.
func (s *Service) List(ctx context.Context, pageSize int32, token string) ([]*repository.Product, string, error) {
	cur, err := pagination.Decode(token)
	if err != nil {
		return nil, "", err
	}
	n := validate.PageSize(pageSize)
	ps, err := s.store.ListActive(ctx, n+1, cur)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if int32(len(ps)) > n {
		ps = ps[:n]
		last := ps[len(ps)-1]
		next = pagination.Encode(pagination.Cursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	return ps, next, nil
}

// UpdateInput carries optional changes.
type UpdateInput struct {
	ID    string
	Patch repository.Patch
}

// Update validates and applies a partial update.
func (s *Service) Update(ctx context.Context, in UpdateInput) (*repository.Product, error) {
	if err := validate.ID("id", in.ID); err != nil {
		return nil, err
	}
	p := in.Patch
	if p.Name != nil {
		v, err := validate.Text("name", *p.Name, 1, 200)
		if err != nil {
			return nil, err
		}
		p.Name = &v
	}
	if p.Description != nil {
		v, err := validate.Text("description", *p.Description, 0, 5000)
		if err != nil {
			return nil, err
		}
		p.Description = &v
	}
	if p.ImageURL != nil {
		v, err := validate.URL("image_url", *p.ImageURL)
		if err != nil {
			return nil, err
		}
		p.ImageURL = &v
	}
	if p.PriceCents != nil {
		if err := validate.PriceCents(*p.PriceCents); err != nil {
			return nil, err
		}
	}
	prod, err := s.store.Update(ctx, in.ID, p)
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, in.ID) // after commit
	return prod, nil
}

// Delete soft-deletes a product.
func (s *Service) Delete(ctx context.Context, id string) error {
	if err := validate.ID("id", id); err != nil {
		return err
	}
	if err := s.store.Deactivate(ctx, id); err != nil {
		return err
	}
	s.invalidate(ctx, id)
	return nil
}

func privileged(ctx context.Context) bool {
	p, ok := auth.FromContext(ctx)
	return ok && (p.IsAdmin() || p.IsService())
}

// --- cache-aside -----------------------------------------------------------

func cacheKey(id string) string { return "product:v1:" + id }

func (s *Service) getCached(ctx context.Context, id string) (*repository.Product, error) {
	if s.cache != nil {
		if b, err := s.cache.Get(ctx, cacheKey(id)).Bytes(); err == nil {
			var p repository.Product
			if json.Unmarshal(b, &p) == nil {
				cacheOps.WithLabelValues("hit").Inc()
				return &p, nil
			}
		} else if !errors.Is(err, redis.Nil) {
			cacheOps.WithLabelValues("error").Inc()
			logx.From(ctx).Warn("product cache get failed", "error", err)
		}
		cacheOps.WithLabelValues("miss").Inc()
	}
	// singleflight collapses concurrent misses for the same id into one query.
	v, err, _ := s.group.Do(id, func() (any, error) {
		// Detach from the first caller's cancellation (other callers share the
		// result) but keep a bound on the query.
		qctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		p, err := s.store.Get(qctx, id)
		if err != nil {
			return nil, err
		}
		s.cacheSet(ctx, p)
		return p, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*repository.Product), nil
}

func (s *Service) cacheMGet(ctx context.Context, ids []string) (found []*repository.Product, missing []string) {
	if s.cache == nil {
		return nil, ids
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = cacheKey(id)
	}
	vals, err := s.cache.MGet(ctx, keys...).Result()
	if err != nil {
		cacheOps.WithLabelValues("error").Inc()
		return nil, ids
	}
	for i, v := range vals {
		str, ok := v.(string)
		var p repository.Product
		if ok && json.Unmarshal([]byte(str), &p) == nil {
			found = append(found, &p)
			cacheOps.WithLabelValues("hit").Inc()
			continue
		}
		missing = append(missing, ids[i])
		cacheOps.WithLabelValues("miss").Inc()
	}
	return found, missing
}

func (s *Service) cacheSet(ctx context.Context, p *repository.Product) {
	if s.cache == nil {
		return
	}
	b, err := json.Marshal(p)
	if err != nil {
		return
	}
	if err := s.cache.Set(ctx, cacheKey(p.ID), b, s.ttl).Err(); err != nil {
		logx.From(ctx).Warn("product cache set failed", "error", err)
	}
}

func (s *Service) invalidate(ctx context.Context, id string) {
	if s.cache == nil {
		return
	}
	if err := s.cache.Del(ctx, cacheKey(id)).Err(); err != nil {
		// The TTL bounds staleness if invalidation fails.
		logx.From(ctx).Warn("product cache invalidation failed", "product_id", id, "error", err)
	}
}
