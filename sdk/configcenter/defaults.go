package configcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// EnsureDefaults atomically creates missing keys in the selected backend and reloads the snapshot.
// Remote initialization requires a server with the defaults endpoint and config:write permission.
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
	if err := c.Load(ctx); err != nil {
		return err
	}
	missing := false
	for key := range defaults {
		if _, ok := c.Get(key); !ok {
			missing = true
			break
		}
	}
	if !missing {
		return nil
	}
	path, err := c.environmentPath(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{"defaults": defaults})
	if err != nil {
		return fmt.Errorf("encode defaults: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, path+"/defaults", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build defaults request: %w", err)
	}
	req.Header.Set("Authorization", "ApiKey "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("send defaults request: %w", err)
	}
	if err := resp.Body.Close(); err != nil {
		return fmt.Errorf("close defaults response: %w", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("configcenter defaults endpoint: status %d (requires config:write and a server supporting initialization)", resp.StatusCode)
	}
	return c.Load(ctx)
}
