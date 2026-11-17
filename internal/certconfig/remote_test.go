package certconfig

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OXeu/certcenter/internal/configcenter"
)

func TestReadMissingSettingNamesKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"items": []configcenter.Item{}})
	}))
	defer server.Close()
	client, err := configcenter.New(configcenter.Config{BaseURL: server.URL, APIKey: "cc_test"})
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	err = NewRemote(client).Read(t.Context(), "certcenter.server", &out)
	if !errors.Is(err, ErrMissing) || !strings.Contains(err.Error(), `"certcenter.server"`) {
		t.Fatal("missing key context", err)
	}
}
