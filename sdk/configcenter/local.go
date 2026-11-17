package configcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

// DefaultLocalPath is the local fallback file relative to the working directory.
const DefaultLocalPath = ".tmp/configcenter.kv"

type localStore struct {
	mu          sync.Mutex
	path        string
	environment string
	defaults    map[string]string
}

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,190}$`)

func validateKey(key string) error {
	if !keyPattern.MatchString(key) {
		return errors.New("configcenter: key must be 1-191 letters, digits or . _ : / - and start with a letter or digit")
	}
	return nil
}

func newLocalStore(path, environment string, defaults map[string]string) (*localStore, error) {
	if path == "" {
		path = DefaultLocalPath
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("configcenter: resolve local KV path: %w", err)
	}
	initial := make(map[string]string, len(defaults))
	for key, value := range defaults {
		if err := validateKey(key); err != nil {
			return nil, err
		}
		initial[key] = value
	}
	return &localStore{path: absolute, environment: environment, defaults: initial}, nil
}

func (s *localStore) transaction(ctx context.Context, fn func(*bolt.Bucket) error) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("configcenter: local operation cancelled: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("configcenter: create local KV directory: %w", err)
	}
	timeout := time.Second
	if deadline, ok := ctx.Deadline(); ok {
		timeout = max(time.Millisecond, min(timeout, time.Until(deadline)))
	}
	db, err := bolt.Open(s.path, 0o600, &bolt.Options{Timeout: timeout})
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("configcenter: wait for local KV: %w", ctx.Err())
		}
		return fmt.Errorf("configcenter: open local KV: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("configcenter: close local KV: %w", err))
		}
	}()
	err = db.Update(func(tx *bolt.Tx) error {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("local transaction cancelled: %w", err)
		}
		root, err := tx.CreateBucketIfNotExists([]byte("configcenter.v1"))
		if err != nil {
			return fmt.Errorf("create local namespace: %w", err)
		}
		bucket, err := root.CreateBucketIfNotExists([]byte(s.environment))
		if err != nil {
			return fmt.Errorf("create local environment: %w", err)
		}
		for key, value := range s.defaults {
			if bucket.Get([]byte(key)) != nil {
				continue
			}
			item := Item{Key: key, Value: value, UpdatedAt: time.Now().UTC()}
			if err := writeLocalItem(bucket, item); err != nil {
				return err
			}
		}
		if err := fn(bucket); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("local transaction cancelled: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("configcenter: local KV transaction: %w", err)
	}
	return nil
}

func writeLocalItem(bucket *bolt.Bucket, item Item) error {
	encoded, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("encode local configuration: %w", err)
	}
	if err := bucket.Put([]byte(item.Key), encoded); err != nil {
		return fmt.Errorf("persist local configuration: %w", err)
	}
	return nil
}

func (s *localStore) read(ctx context.Context, prefix string) ([]Item, error) {
	items := []Item{}
	err := s.transaction(ctx, func(bucket *bolt.Bucket) error {
		cursor := bucket.Cursor()
		for key, value := cursor.Seek([]byte(prefix)); key != nil && bytes.HasPrefix(key, []byte(prefix)); key, value = cursor.Next() {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("read local configuration: %w", err)
			}
			var item Item
			if err := json.Unmarshal(value, &item); err != nil {
				return errors.New("invalid local configuration record")
			}
			if item.Key != string(key) {
				return errors.New("local configuration key mismatch")
			}
			items = append(items, item)
		}
		return nil
	})
	return items, err
}

func (s *localStore) put(ctx context.Context, key, value string) (Item, error) {
	item := Item{Key: key, Value: value, UpdatedAt: time.Now().UTC()}
	err := s.transaction(ctx, func(bucket *bolt.Bucket) error { return writeLocalItem(bucket, item) })
	return item, err
}
