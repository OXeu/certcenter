package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/OXeu/certcenter/internal/auth"
	"github.com/OXeu/certcenter/internal/store"
	"github.com/gin-gonic/gin"
)

const mcpScope = "mcp"

func credentialHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func (s *Server) oauthIssuer() string { return strings.TrimRight(s.cfg.PublicURL, "/") }
func (s *Server) mcpResource() string { return s.oauthIssuer() + "/mcp" }

func (s *Server) mountMCPOAuth(r *gin.Engine) {
	r.GET("/.well-known/oauth-protected-resource/mcp", s.mcpResourceMetadata)
	r.GET("/.well-known/oauth-protected-resource", s.mcpResourceMetadata)
	r.GET("/.well-known/oauth-authorization-server", s.mcpAuthorizationMetadata)
	r.POST("/oauth/register", s.mcpRegister)
	r.POST("/oauth/token", s.mcpToken)
	r.POST("/oauth/revoke", s.mcpRevoke)
	r.GET("/oauth/authorize", s.mcpAuthorize)
	r.POST("/oauth/authorize", auth.RequireUser(), s.mcpConsent)
}

func (s *Server) mcpResourceMetadata(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"resource": s.mcpResource(), "authorization_servers": []string{s.oauthIssuer()}, "scopes_supported": []string{mcpScope}, "bearer_methods_supported": []string{"header"}})
}

func (s *Server) mcpAuthorizationMetadata(c *gin.Context) {
	issuer := s.oauthIssuer()
	c.JSON(http.StatusOK, gin.H{"issuer": issuer, "authorization_endpoint": issuer + "/oauth/authorize", "token_endpoint": issuer + "/oauth/token", "registration_endpoint": issuer + "/oauth/register", "revocation_endpoint": issuer + "/oauth/revoke", "response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "token_endpoint_auth_methods_supported": []string{"none"}, "code_challenge_methods_supported": []string{"S256"}, "scopes_supported": []string{mcpScope}})
}

func oauthError(c *gin.Context, status int, code string) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	c.AbortWithStatusJSON(status, gin.H{"error": code})
}

func (s *Server) mcpRegister(c *gin.Context) {
	var in struct {
		Name          string   `json:"client_name"`
		RedirectURIs  []string `json:"redirect_uris"`
		GrantTypes    []string `json:"grant_types"`
		ResponseTypes []string `json:"response_types"`
		AuthMethod    string   `json:"token_endpoint_auth_method"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	if err := c.ShouldBindJSON(&in); err != nil {
		oauthError(c, 400, "invalid_client_metadata")
		return
	}
	if len(in.Name) > 128 || len(in.RedirectURIs) == 0 || len(in.RedirectURIs) > 10 || (in.AuthMethod != "" && in.AuthMethod != "none") {
		oauthError(c, 400, "invalid_client_metadata")
		return
	}
	for _, grant := range in.GrantTypes {
		if grant != "authorization_code" && grant != "refresh_token" {
			oauthError(c, 400, "invalid_client_metadata")
			return
		}
	}
	for _, response := range in.ResponseTypes {
		if response != "code" {
			oauthError(c, 400, "invalid_client_metadata")
			return
		}
	}
	if in.Name == "" {
		in.Name = "MCP client"
	}
	client := store.OAuthClient{ID: rand.Text(), Name: in.Name, RedirectURIs: in.RedirectURIs, CreatedAt: time.Now().UTC()}
	if err := s.store.CreateOAuthClient(c.Request.Context(), client); err != nil {
		s.fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, gin.H{"client_id": client.ID, "client_name": client.Name, "redirect_uris": client.RedirectURIs, "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}, "token_endpoint_auth_method": "none"})
}

func (s *Server) mcpAuthenticate(c *gin.Context) {
	fields := strings.Fields(c.GetHeader("Authorization"))
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
		s.mcpUnauthorized(c)
		return
	}
	user, err := s.store.OAuthAccessUser(c.Request.Context(), credentialHash(fields[1]), s.mcpResource())
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.fail(c, err)
			c.Abort()
			return
		}
		s.mcpUnauthorized(c)
		return
	}
	c.Request = c.Request.WithContext(auth.WithIdentity(c.Request.Context(), auth.Identity{User: &user, Role: user.Role}))
	c.Next()
}

func (s *Server) mcpUnauthorized(c *gin.Context) {
	c.Header("WWW-Authenticate", `Bearer resource_metadata="`+s.oauthIssuer()+`/.well-known/oauth-protected-resource/mcp", scope="mcp"`)
	oauthError(c, http.StatusUnauthorized, "invalid_token")
}

func (s *Server) mcpAuthorize(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	q := c.Request.URL.Query()
	for _, values := range q {
		if len(values) != 1 {
			oauthError(c, 400, "invalid_request")
			return
		}
	}
	client, err := s.store.OAuthClientByID(c.Request.Context(), q.Get("client_id"))
	if err != nil || !slices.Contains(client.RedirectURIs, q.Get("redirect_uri")) {
		oauthError(c, 400, "invalid_request")
		return
	}
	challenge, err := base64.RawURLEncoding.DecodeString(q.Get("code_challenge"))
	if q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || err != nil || len(challenge) != 32 || len(q.Get("code_challenge")) != 43 || len(q.Get("state")) > 2048 {
		oauthError(c, 400, "invalid_request")
		return
	}
	if q.Get("resource") != s.mcpResource() {
		oauthError(c, 400, "invalid_target")
		return
	}
	if q.Get("scope") != "" && q.Get("scope") != mcpScope {
		oauthError(c, 400, "invalid_scope")
		return
	}
	request := store.OAuthAuthorization{ID: rand.Text(), ClientID: client.ID, RedirectURI: q.Get("redirect_uri"), Resource: s.mcpResource(), Scope: mcpScope, State: q.Get("state"), Challenge: q.Get("code_challenge"), Status: "login", ExpiresAt: time.Now().UTC().Add(10 * time.Minute)}
	id, ok := auth.FromContext(c.Request.Context())
	if !ok || id.User == nil {
		if s.oauth == nil {
			oauthError(c, 503, "temporarily_unavailable")
			return
		}
		if err := s.store.CreateOAuthAuthorization(c.Request.Context(), request); err != nil {
			s.fail(c, err)
			return
		}
		http.SetCookie(c.Writer, &http.Cookie{Name: "cc_oauth_return", Value: request.ID, Path: "/oauth/callback", HttpOnly: true, Secure: strings.HasPrefix(s.oauthIssuer(), "https://"), SameSite: http.SameSiteLaxMode, MaxAge: 600})
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}
	session, err := c.Cookie("cc_session")
	if err != nil {
		oauthError(c, 401, "login_required")
		return
	}
	request.UserID, request.SessionHash, request.Status = id.User.ID, credentialHash(session), "pending"
	if err := s.store.CreateOAuthAuthorization(c.Request.Context(), request); err != nil {
		s.fail(c, err)
		return
	}
	s.render(c, http.StatusOK, gin.H{"Page": "mcp-consent", "Title": "mcp.authorize", "Consent": gin.H{"request": request.ID, "client": client.Name, "redirectUri": request.RedirectURI, "resource": request.Resource}})
}

func (s *Server) mcpConsent(c *gin.Context) {
	var in struct {
		Request string `json:"request"`
		Approve *bool  `json:"approve"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	if err := c.ShouldBindJSON(&in); err != nil || in.Approve == nil {
		oauthError(c, 400, "invalid_request")
		return
	}
	session, err := c.Cookie("cc_session")
	if err != nil {
		oauthError(c, 401, "login_required")
		return
	}
	id, _ := auth.FromContext(c.Request.Context())
	request, err := s.store.OAuthAuthorizationByID(c.Request.Context(), in.Request, id.User.ID, credentialHash(session))
	if err != nil {
		oauthError(c, 400, "invalid_request")
		return
	}
	redirect, err := url.Parse(request.RedirectURI)
	if err != nil {
		oauthError(c, 400, "invalid_request")
		return
	}
	query := redirect.Query()
	query.Set("state", request.State)
	status, code := "denied", ""
	if *in.Approve {
		status, code = "approved", rand.Text()
		query.Set("code", code)
	} else {
		query.Set("error", "access_denied")
	}
	if err := s.store.DecideOAuthAuthorization(c.Request.Context(), request.ID, request.SessionHash, credentialHash(code), status); err != nil {
		oauthError(c, 400, "invalid_request")
		return
	}
	redirect.RawQuery = query.Encode()
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"redirect": redirect.String()})
}

func validVerifier(value string) bool {
	if len(value) < 43 || len(value) > 128 {
		return false
	}
	for _, ch := range value {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~", ch) {
			return false
		}
	}
	return true
}

func oauthForm(c *gin.Context) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	if !strings.HasPrefix(c.GetHeader("Content-Type"), "application/x-www-form-urlencoded") {
		oauthError(c, 400, "invalid_request")
		return false
	}
	if err := c.Request.ParseForm(); err != nil {
		oauthError(c, 400, "invalid_request")
		return false
	}
	for _, values := range c.Request.PostForm {
		if len(values) != 1 {
			oauthError(c, 400, "invalid_request")
			return false
		}
	}
	return true
}

func (s *Server) mcpToken(c *gin.Context) {
	if !oauthForm(c) {
		return
	}
	form := c.Request.PostForm
	if _, err := s.store.OAuthClientByID(c.Request.Context(), form.Get("client_id")); err != nil {
		oauthError(c, 400, "invalid_client")
		return
	}
	if form.Get("resource") != s.mcpResource() {
		oauthError(c, 400, "invalid_target")
		return
	}
	if scope := form.Get("scope"); scope != "" && scope != mcpScope {
		oauthError(c, 400, "invalid_scope")
		return
	}
	access, refresh := rand.Text(), rand.Text()
	token := store.OAuthToken{AccessHash: credentialHash(access), RefreshHash: credentialHash(refresh), AccessExpiresAt: time.Now().UTC().Add(time.Hour), RefreshExpiresAt: time.Now().UTC().Add(30 * 24 * time.Hour)}
	var err error
	switch form.Get("grant_type") {
	case "authorization_code":
		verifier := form.Get("code_verifier")
		if !validVerifier(verifier) {
			oauthError(c, 400, "invalid_grant")
			return
		}
		hash := sha256.Sum256([]byte(verifier))
		err = s.store.ExchangeOAuthCode(c.Request.Context(), credentialHash(form.Get("code")), form.Get("client_id"), form.Get("redirect_uri"), form.Get("resource"), base64.RawURLEncoding.EncodeToString(hash[:]), &token)
	case "refresh_token":
		err = s.store.RefreshOAuthToken(c.Request.Context(), credentialHash(form.Get("refresh_token")), form.Get("client_id"), form.Get("resource"), &token)
	default:
		oauthError(c, 400, "unsupported_grant_type")
		return
	}
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.log.Error("oauth token exchange failed", "error", err)
		}
		oauthError(c, 400, "invalid_grant")
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	c.JSON(http.StatusOK, gin.H{"access_token": access, "token_type": "Bearer", "expires_in": 3600, "refresh_token": refresh, "scope": token.Scope})
}

func (s *Server) mcpRevoke(c *gin.Context) {
	if !oauthForm(c) {
		return
	}
	form := c.Request.PostForm
	if err := s.store.RevokeOAuthToken(c.Request.Context(), credentialHash(form.Get("token")), form.Get("client_id")); err != nil {
		s.fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Status(http.StatusOK)
}

func authorizationURL(request store.OAuthAuthorization) string {
	return "/oauth/authorize?" + url.Values{
		"client_id": {request.ClientID}, "redirect_uri": {request.RedirectURI},
		"resource": {request.Resource}, "scope": {request.Scope}, "state": {request.State},
		"response_type": {"code"}, "code_challenge_method": {"S256"}, "code_challenge": {request.Challenge},
	}.Encode()
}
