package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OXeu/certcenter/internal/auth"
	"github.com/OXeu/certcenter/internal/store"
	"github.com/gin-gonic/gin"
)

func TestProjectOwnership(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name    string
		user    *store.User
		allowed bool
	}{
		{"anonymous", nil, false},
		{"owner", &store.User{ID: 1, Role: store.RoleUser}, true},
		{"other", &store.User{ID: 2, Role: store.RoleUser}, false},
		{"administrator", &store.User{ID: 2, Role: store.RoleAdmin}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			c.Request = c.Request.WithContext(auth.WithIdentity(context.Background(), auth.Identity{User: tc.user}))
			s := &Server{}
			if got := s.allowProject(c, store.Project{OwnerID: 1}); got != tc.allowed {
				t.Fatalf("allowed = %v, want %v", got, tc.allowed)
			}
		})
	}
}
