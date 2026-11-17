package configcenter

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func localTestPath(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".tmp", "config-kv-"+rand.Text()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	return filepath.Join(root, "settings.kv")
}

func localClient(t *testing.T, cfg Config) *Client {
	t.Helper()
	client, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestLocalPersistenceEnvironmentIsolationAndPrefix(t *testing.T) {
	path := localTestPath(t)
	client := localClient(t, Config{LocalPath: path, LocalDefaults: map[string]string{"db.pool": "5", "feature.enabled": "true"}})
	if !client.IsLocal() {
		t.Fatal("unconfigured SDK did not select local KV")
	}
	if err := client.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if client.Int("db.pool", 0) != 5 || !client.Bool("feature.enabled", false) {
		t.Fatal("defaults not initialized")
	}
	if err := client.Put(t.Context(), "db.pool", "20"); err != nil {
		t.Fatal(err)
	}
	if client.Int("db.pool", 0) != 20 {
		t.Fatal("snapshot not updated after write")
	}
	reopened := localClient(t, Config{LocalPath: path, LocalDefaults: map[string]string{"db.pool": "99"}})
	if err := reopened.LoadPrefix(t.Context(), "db."); err != nil {
		t.Fatal(err)
	}
	if reopened.Int("db.pool", 0) != 20 || len(reopened.All()) != 1 || reopened.All()[0].UpdatedAt.IsZero() {
		t.Fatal("persisted record or prefix incorrect")
	}
	if err := reopened.Put(t.Context(), "feature.enabled", "false"); err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.Get("feature.enabled"); ok {
		t.Fatal("write polluted prefix snapshot")
	}
	staging := localClient(t, Config{LocalPath: path, Environment: "staging"})
	if err := staging.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(staging.All()) != 0 {
		t.Fatal("environment data leaked")
	}
	if err := staging.Put(t.Context(), "db.pool", "3"); err != nil {
		t.Fatal(err)
	}
	if err := client.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if client.Int("db.pool", 0) != 20 || client.Bool("feature.enabled", true) {
		t.Fatal("wrong environment or stale reload")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatal("local KV has broad permissions")
	}
}

func TestLocalDefaultsAreAtomicAcrossClients(t *testing.T) {
	path := localTestPath(t)
	var wg sync.WaitGroup
	errorsOut := make(chan error, 8)
	values := make(chan string, 8)
	for i := range 8 {
		wg.Go(func() {
			client, err := New(Config{LocalPath: path, LocalDefaults: map[string]string{"encryption.key": fmt.Sprintf("key-%d", i)}})
			if err != nil {
				errorsOut <- err
				return
			}
			if err := client.Load(t.Context()); err != nil {
				errorsOut <- err
				return
			}
			values <- client.GetDefault("encryption.key", "")
		})
	}
	wg.Wait()
	close(errorsOut)
	close(values)
	for err := range errorsOut {
		t.Error(err)
	}
	winner := ""
	for value := range values {
		if winner == "" {
			winner = value
		}
		if value != winner {
			t.Fatal("concurrent startup replaced an existing default")
		}
	}
	if winner == "" {
		t.Fatal("defaults not persisted")
	}
}

func TestLocalConcurrentWritesAndCancellation(t *testing.T) {
	path := localTestPath(t)
	client := localClient(t, Config{LocalPath: path})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := client.Put(ctx, "key", "value"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled write", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancelled operation created a database")
	}
	var wg sync.WaitGroup
	errorsOut := make(chan error, 32)
	for i := range 32 {
		wg.Go(func() { errorsOut <- client.Put(t.Context(), fmt.Sprintf("key.%02d", i), "value") })
	}
	wg.Wait()
	close(errorsOut)
	for err := range errorsOut {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := client.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(client.All()) != 32 {
		t.Fatal("lost concurrent writes")
	}
}

func TestRemoteErrorsNeverFallBackToLocalKV(t *testing.T) {
	path := localTestPath(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer server.Close()
	client := localClient(t, Config{BaseURL: server.URL, APIKey: "cc_test", LocalPath: path, LocalDefaults: map[string]string{"key": "local"}})
	if client.IsLocal() {
		t.Fatal("configured client selected local KV")
	}
	if err := client.Load(t.Context()); err == nil {
		t.Fatal("remote failure hidden")
	}
	if err := client.Put(t.Context(), "key", "value"); err == nil {
		t.Fatal("remote write redirected locally")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("remote failure created a local database")
	}
	for _, cfg := range []Config{{BaseURL: server.URL, LocalPath: path}, {APIKey: "cc_test", LocalPath: path}} {
		if _, err := New(cfg); err == nil {
			t.Fatal("partial remote configuration silently fell back")
		}
	}
}

func TestLocalWatchRefreshesAnotherClientWrites(t *testing.T) {
	path := localTestPath(t)
	reader := localClient(t, Config{LocalPath: path})
	writer := localClient(t, Config{LocalPath: path})
	ctx, cancel := context.WithCancel(t.Context())
	defer func() {
		cancel()
		reader.local.mu.Lock()
		defer reader.local.mu.Unlock()
	}()
	if err := reader.Watch(ctx, WatchOptions{Interval: 5 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Put(ctx, "watched", "updated"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if reader.GetDefault("watched", "") == "updated" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("local watcher did not refresh")
}

func TestCorruptLocalDatabaseIsNotReplaced(t *testing.T) {
	path := localTestPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := localClient(t, Config{LocalPath: path})
	if err := client.Load(t.Context()); err == nil {
		t.Fatal("corrupt KV accepted")
	}
	if err := client.Put(t.Context(), "key", "value"); err == nil {
		t.Fatal("corrupt KV overwritten")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "corrupt" {
		t.Fatal("corrupt file was replaced", err)
	}
}
