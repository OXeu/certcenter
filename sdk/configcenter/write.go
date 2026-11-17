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
)

// Put writes a value to the single project bound to this client's API key.
// The key needs projects:read, environments:read and config:write permissions.
func (c *Client) Put(ctx context.Context, key, value string) error {
	var scope struct {
		Project struct {
			Slug string `json:"slug"`
		} `json:"project"`
	}
	if err := c.get(ctx, "/project", nil, &scope); err != nil {
		return err
	}
	if scope.Project.Slug == "" {
		return errors.New("configcenter: missing project scope")
	}
	body, err := json.Marshal(map[string]string{"value": value})
	if err != nil {
		return fmt.Errorf("configcenter: encode value: %w", err)
	}
	target := strings.TrimSuffix(c.endpoint, "/agent") + "/projects/" + url.PathEscape(scope.Project.Slug) + "/environments/" + url.PathEscape(c.env) + "/items/" + url.PathEscape(key)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, target, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("configcenter: create write request: %w", err)
	}
	req.Header.Set("Authorization", "ApiKey "+c.apiKey)
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
	return nil
}
