package store

import (
	"context"
	"database/sql"
	"errors"

	"wishd/internal/model"
)

func (s *Store) Categories(ctx context.Context) ([]*model.Category, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, slug, label, sort_order, field_schema FROM categories ORDER BY sort_order, label`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Category
	for rows.Next() {
		var c model.Category
		if err := rows.Scan(&c.ID, &c.Slug, &c.Label, &c.SortOrder, &c.FieldSchema); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

func (s *Store) CategoryByID(ctx context.Context, id string) (*model.Category, error) {
	var c model.Category
	err := s.db.QueryRowContext(ctx,
		`SELECT id, slug, label, sort_order, field_schema FROM categories WHERE id = ?`, id).
		Scan(&c.ID, &c.Slug, &c.Label, &c.SortOrder, &c.FieldSchema)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, model.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) CategoryBySlug(ctx context.Context, slug string) (*model.Category, error) {
	var c model.Category
	err := s.db.QueryRowContext(ctx,
		`SELECT id, slug, label, sort_order, field_schema FROM categories WHERE slug = ?`, slug).
		Scan(&c.ID, &c.Slug, &c.Label, &c.SortOrder, &c.FieldSchema)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, model.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}
