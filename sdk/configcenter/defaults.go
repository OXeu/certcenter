package configcenter

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// EnsureDefaults creates missing keys in the selected backend and reloads the snapshot.
// Remote initialization uses batch reads and writes; run bootstrap from one instance.
// It requires config:read, config:write, projects:read and environments:read permissions.
func (c *Client) EnsureDefaults(ctx context.Context, defaults map[string]string) error {
	keys := make([]string, 0, len(defaults))
	for key := range defaults {
		if err := validateKey(key); err != nil {
			return fmt.Errorf("initialize default key %q: %w", key, err)
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil
	}
	sort.Strings(keys)
	if err := c.ensureDefaults(ctx, defaults); err != nil {
		return fmt.Errorf("initialize default keys [%s]: %w", strings.Join(keys, ", "), err)
	}
	return nil
}

func (c *Client) ensureDefaults(ctx context.Context, defaults map[string]string) error {
	if c.local != nil {
		c.local.mu.Lock()
		defer c.local.mu.Unlock()
		if err := c.local.transaction(ctx, func(bucket *bolt.Bucket) error {
			for key, value := range defaults {
				if bucket.Get([]byte(key)) != nil {
					continue
				}
				if err := writeLocalItem(bucket, Item{Key: key, Value: value, UpdatedAt: time.Now().UTC()}); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
		items, err := c.local.read(ctx, "")
		if err != nil {
			return err
		}
		c.replace(items, "")
		return nil
	}
	existing, err := c.readMany(ctx, batchKeys(defaults))
	if err != nil {
		return err
	}
	missing := make(map[string]string)
	for key, value := range defaults {
		if _, ok := existing[key]; !ok {
			missing[key] = value
		}
	}
	if len(missing) > 0 {
		if err := c.putMany(ctx, missing); err != nil {
			return err
		}
	}
	return c.Load(ctx)
}
