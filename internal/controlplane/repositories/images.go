package repositories

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/tuna2134/vps/internal/controlplane/models"
)

type ImageRepository struct {
	db DBTX
}

func NewImageRepository(db DBTX) *ImageRepository {
	return &ImageRepository{db: db}
}

const imageCols = `id, name, version, architecture, format, source_url, checksum, size_bytes,
	cloud_init_compatible, status, created_at, updated_at`

func scanImage(row pgx.Row) (*models.Image, error) {
	var i models.Image
	err := row.Scan(&i.ID, &i.Name, &i.Version, &i.Architecture, &i.Format, &i.SourceURL,
		&i.Checksum, &i.SizeBytes, &i.CloudInitCompatible, &i.Status, &i.CreatedAt, &i.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &i, nil
}

func (r *ImageRepository) Create(ctx context.Context, img *models.Image) (*models.Image, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO images (name, version, architecture, format, source_url, checksum, size_bytes, cloud_init_compatible)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING `+imageCols,
		img.Name, img.Version, img.Architecture, img.Format, img.SourceURL,
		img.Checksum, img.SizeBytes, img.CloudInitCompatible)
	created, err := scanImage(row)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("create image: %w", ErrConflict)
		}
		return nil, fmt.Errorf("create image: %w", err)
	}
	return created, nil
}

func (r *ImageRepository) GetByID(ctx context.Context, id string) (*models.Image, error) {
	row := r.db.QueryRow(ctx, `SELECT `+imageCols+` FROM images WHERE id = $1`, id)
	img, err := scanImage(row)
	if isNoRowsOrInvalidUUID(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get image: %w", err)
	}
	return img, nil
}

func (r *ImageRepository) List(ctx context.Context, activeOnly bool) ([]models.Image, error) {
	q := `SELECT ` + imageCols + ` FROM images`
	if activeOnly {
		q += ` WHERE status = 'active'`
	}
	q += ` ORDER BY name, version`
	rows, err := r.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	defer rows.Close()
	var out []models.Image
	for rows.Next() {
		img, err := scanImage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *img)
	}
	return out, rows.Err()
}
