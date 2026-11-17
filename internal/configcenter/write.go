package configcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Put persists a value and updates the loaded snapshot if its prefix includes key.
// Remote writes need projects:read, environments:read and config:write permissions.
func (c *Client) Put(ctx context.Context, key, value string) error {
	if err := c.put(ctx, key, value); err != nil {
		return fmt.Errorf("write configuration key %q: %w", key, err)
	}
	return nil
}

func (c *Client) put(ctx context.Context, key, value string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if c.local != nil {
		c.local.mu.Lock()
		defer c.local.mu.Unlock()
		item, err := c.local.put(ctx, key, value)
		if err != nil {
			return err
		}
		c.cachePut(item)
		return nil
	}
	path, err := c.environmentPath(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{"value": value})
	if err != nil {
		return fmt.Errorf("configcenter: encode value: %w", err)
	}
	target := path + "/items/" + url.PathEscape(key)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, target, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("configcenter: create write request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("configcenter: write request: %w", err)
	}
	if err := resp.Body.Close(); err != nil {
		return fmt.Errorf("configcenter: close write response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("configcenter: write status %d", resp.StatusCode)
	}
	c.cachePut(Item{Key: key, Value: value, UpdatedAt: time.Now().UTC()})
	return nil
}

func (c *Client) environmentPath(ctx context.Context) (string, error) {
	var scope struct {
		Project struct {
			Slug string `json:"slug"`
		} `json:"project"`
	}
	if err := c.get(ctx, "/project", nil, &scope); err != nil {
		return "", err
	}
	if scope.Project.Slug == "" {
		return "", errors.New("configcenter: missing project scope")
	}
	return strings.TrimSuffix(c.endpoint, "/agent") + "/projects/" + url.PathEscape(scope.Project.Slug) + "/environments/" + url.PathEscape(c.env), nil
}

func (c *Client) cachePut(item Item) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !strings.HasPrefix(item.Key, c.prefix) {
		return
	}
	c.items[item.Key] = item.Value
	for i := range c.order {
		if c.order[i].Key == item.Key {
			c.order[i] = item
			return
		}
	}
	c.order = append(c.order, item)
}
