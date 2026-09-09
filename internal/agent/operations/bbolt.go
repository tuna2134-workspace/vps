package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.etcd.io/bbolt"
)

var (
	ErrNotFound = errors.New("operation not found")
	ErrConflict = errors.New("operation already exists")
)

var operationsBucket = []byte("operations")

// BoltStore persists operations in a single-file bbolt database. bbolt is a
// pure-Go, ACID transactional key-value store, so idempotency survives agent
// restarts without a CGO dependency.
type BoltStore struct {
	db *bbolt.DB
}

// OpenBolt opens (creating if needed) the operation database at path.
func OpenBolt(path string) (*BoltStore, error) {
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, fmt.Errorf("open operations db: %w", err)
	}
	if err := db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(operationsBucket)
		return err
	}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init operations db: %w", err)
	}
	return &BoltStore{db: db}, nil
}

// Close closes the underlying database.
func (s *BoltStore) Close() error {
	return s.db.Close()
}

func (s *BoltStore) Get(ctx context.Context, id string) (*Operation, error) {
	var out *Operation
	err := s.db.View(func(tx *bbolt.Tx) error {
		v := tx.Bucket(operationsBucket).Get([]byte(id))
		if v == nil {
			return ErrNotFound
		}
		var op Operation
		if err := json.Unmarshal(v, &op); err != nil {
			return err
		}
		out = &op
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get operation: %w", err)
	}
	return out, nil
}

func (s *BoltStore) Create(ctx context.Context, op *Operation) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(operationsBucket)
		if b.Get([]byte(op.ID)) != nil {
			return ErrConflict
		}
		return putOperation(b, op)
	})
}

func (s *BoltStore) MarkRunning(ctx context.Context, id string) error {
	return s.updateState(ctx, id, StateRunning, "")
}

func (s *BoltStore) MarkSucceeded(ctx context.Context, id string) error {
	return s.updateState(ctx, id, StateSucceeded, "")
}

func (s *BoltStore) MarkFailed(ctx context.Context, id, message string) error {
	return s.updateState(ctx, id, StateFailed, message)
}

func (s *BoltStore) updateState(ctx context.Context, id string, state State, msg string) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(operationsBucket)
		v := b.Get([]byte(id))
		if v == nil {
			return ErrNotFound
		}
		var op Operation
		if err := json.Unmarshal(v, &op); err != nil {
			return err
		}
		op.State = state
		op.Error = msg
		op.UpdatedAt = time.Now().UTC()
		return putOperation(b, &op)
	})
}

func putOperation(b *bbolt.Bucket, op *Operation) error {
	data, err := json.Marshal(op)
	if err != nil {
		return fmt.Errorf("encode operation: %w", err)
	}
	return b.Put([]byte(op.ID), data)
}
