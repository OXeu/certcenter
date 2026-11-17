package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OXeu/certcenter"
)

func TestAcceptsGzip(t *testing.T) {
	for _, test := range []struct {
		header string
		want   bool
	}{
		{"", false},
		{"br, gzip", true},
		{"gzip;q=0", false},
		{"*;q=1, gzip;q=0", false},
		{"gzip;q=0.5", true},
		{"gzip;q=invalid", false},
		{"gzip;q=NaN", false},
		{"*;q=0.8", true},
		{"br", false},
	} {
		if got := acceptsGzip(test.header); got != test.want {
			t.Errorf("acceptsGzip(%q) = %v", test.header, got)
		}
	}
}

func TestStaticCompression(t *testing.T) {
	_, router, _ := testServer(t, false)
	url := frontendCSS(t)
	plain := httptest.NewRecorder()
	router.ServeHTTP(plain, httptest.NewRequest(http.MethodGet, url, http.NoBody))
	for _, encoding := range []string{"gzip", "gzip;q=0", "br", "*"} {
		t.Run(encoding, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, url, http.NoBody)
			request.Header.Set("Accept-Encoding", encoding)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Header().Get("Vary") != "Accept-Encoding" {
				t.Fatalf("unexpected response: %d %v", response.Code, response.Header())
			}
			if response.Header().Get("Cache-Control") != plain.Header().Get("Cache-Control") ||
				response.Header().Get("Content-Type") != plain.Header().Get("Content-Type") {
				t.Fatal("compression changed caching or content type")
			}
			body := response.Body.Bytes()
			if acceptsGzip(encoding) {
				if response.Header().Get("Content-Encoding") != "gzip" || len(body) >= plain.Body.Len() {
					t.Fatal("missing compression")
				}
				reader, err := gzip.NewReader(bytes.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := io.ReadAll(reader)
				closeErr := reader.Close()
				if err != nil {
					t.Fatal(err)
				}
				if closeErr != nil {
					t.Fatal(closeErr)
				}
				body = decoded
			} else if response.Header().Get("Content-Encoding") != "" {
				t.Fatal("unexpected compression")
			}
			if !bytes.Equal(body, plain.Body.Bytes()) {
				t.Fatal("asset content changed")
			}
		})
	}
	request := httptest.NewRequest(http.MethodGet, url, http.NoBody)
	request.Header.Set("Accept-Encoding", "gzip")
	request.Header.Set("Range", "bytes=0-9")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusPartialContent || response.Header().Get("Content-Encoding") != "" || !bytes.Equal(response.Body.Bytes(), plain.Body.Bytes()[:10]) {
		t.Fatal("range response changed")
	}
}

func TestStaticSkipsSessionMiddleware(t *testing.T) {
	_, router, _ := testServer(t, false)
	for _, url := range []string{frontendCSS(t), "/static/brand/icon-512.webp", "/login"} {
		request := httptest.NewRequest(http.MethodGet, url, http.NoBody)
		request.AddCookie(&http.Cookie{Name: "cc_session", Value: "invalid-session"})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		cleared := response.Header().Get("Set-Cookie") != ""
		if cleared != (url == "/login") {
			t.Fatalf("%s session cookie cleared = %v", url, cleared)
		}
	}
}

func frontendCSS(t *testing.T) string {
	t.Helper()
	raw, err := certcenter.Frontend.ReadFile(".tmp/ui/.vite/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]struct{ CSS []string }
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	return "/static/app/" + manifest["web/ui/src/main.ts"].CSS[0]
}
