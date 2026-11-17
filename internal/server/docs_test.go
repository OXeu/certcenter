package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OXeu/certcenter"
)

func TestDocumentation(t *testing.T) {
	_, r, _ := testServer(t, false)
	for _, name := range []string{"index", "api", "sdk", "mcp", "config", "operations"} {
		for _, suffix := range []string{"", ".md"} {
			path := "/docs/" + name + suffix
			t.Run(path, func(t *testing.T) {
				rec := httptest.NewRecorder()
				r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, http.NoBody))
				contentType := "text/html"
				marker := "<h1>"
				if suffix == ".md" {
					contentType = "text/markdown"
					marker = "# "
				}
				if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), contentType) || !strings.Contains(rec.Body.String(), marker) {
					t.Fatalf("unexpected documentation response: %d %s", rec.Code, rec.Header().Get("Content-Type"))
				}
				if suffix == ".md" {
					source, err := certcenter.Documentation.ReadFile("docs/" + name + ".md")
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(source, rec.Body.Bytes()) {
						t.Fatal("Markdown source was modified")
					}
					return
				}
				if !strings.Contains(rec.Body.String(), `/static/app/main-`) || strings.Contains(rec.Body.String(), `/static/docs.css`) {
					t.Fatal("documentation must use the shared stylesheet")
				}
				if !strings.Contains(rec.Body.String(), `/static/docs/shiki.css`) {
					t.Fatal("missing static token stylesheet")
				}
				if name != "index" && (!strings.Contains(rec.Body.String(), `class="shiki `) || !strings.Contains(rec.Body.String(), `shiki-token-`)) {
					t.Fatal("code blocks were not prerendered")
				}
				if strings.Contains(rec.Body.String(), ` style="`) {
					t.Fatal("inline styles violate the CSP")
				}
				if !strings.Contains(rec.Body.String(), `class="document-content"`) {
					t.Fatal("missing server rendered document")
				}
				_, tail, ok := strings.Cut(rec.Body.String(), `<script id="page-data" type="application/json">`)
				if !ok {
					t.Fatal("missing shared bootstrap")
				}
				raw, _, _ := strings.Cut(tail, "</script>")
				var data struct {
					Page     string `json:"page"`
					DocPage  string `json:"docPage"`
					LoggedIn bool   `json:"loggedIn"`
				}
				if err := json.Unmarshal([]byte(raw), &data); err != nil {
					t.Fatal(err)
				}
				if data.Page != "docs" || data.DocPage != name || data.LoggedIn {
					t.Fatalf("invalid public page data: %+v", data)
				}
			})
		}
	}
	for _, path := range []string{"/docs/commits.md", "/docs/missing", "/docs/../README.md"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, http.NoBody))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status %d", path, rec.Code)
		}
	}
}

func TestDocumentationTokenStyles(t *testing.T) {
	_, router, _ := testServer(t, false)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/docs/shiki.css", http.NoBody))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("styles = %d", rec.Code)
	}
	for _, marker := range []string{".shiki-token-", "--shiki-light:", "--shiki-dark:"} {
		if !strings.Contains(rec.Body.String(), marker) {
			t.Fatalf("missing %s", marker)
		}
	}
}
