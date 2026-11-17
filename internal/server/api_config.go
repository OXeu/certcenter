package server

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/OXeu/certcenter/internal/auth"
	"github.com/OXeu/certcenter/internal/slug"
	"github.com/OXeu/certcenter/internal/store"
)

func (s *Server) apiGetItem(c *gin.Context, project store.Project) {
	env, ok := s.resolveEnv(c, project)
	if !ok {
		return
	}
	item, err := s.store.GetItem(c.Request.Context(), project.ID, env.ID, c.Param("key"))
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, toItemJSON(item))
}

func (s *Server) apiCreateProposal(c *gin.Context, project store.Project) {
	var in struct {
		Environment string  `json:"environment"`
		Key         string  `json:"key"`
		Action      string  `json:"action"`
		Value       *string `json:"value"`
		Reason      string  `json:"reason"`
	}
	if err := bindJSON(c, &in); err != nil {
		s.fail(c, err)
		return
	}
	if err := slug.ValidateKey(in.Key); err != nil {
		s.fail(c, err)
		return
	}
	if (in.Action != store.ActionSet && in.Action != store.ActionDelete) || (in.Action == store.ActionSet && in.Value == nil) {
		s.fail(c, fmt.Errorf("%w: action must be set with a value, or delete", errBadRequest))
		return
	}
	env, err := s.store.EnvironmentBySlug(c.Request.Context(), project.ID, in.Environment)
	if err != nil {
		s.fail(c, err)
		return
	}
	id, _ := auth.FromContext(c.Request.Context())
	proposal := store.MCPProposal{
		ProjectID: project.ID, EnvironmentID: env.ID, Key: in.Key, Action: in.Action,
		Reason: in.Reason, Status: store.StatusPending, ProposedBy: id.ActorID(), CreatedAt: time.Now().UTC(),
	}
	if id.APIKey != nil {
		proposal.APIKeyID = id.APIKey.ID
	}
	if in.Action == store.ActionSet {
		proposal.Value = in.Value
	}
	created, err := s.store.CreateProposal(c.Request.Context(), proposal)
	if err != nil {
		s.fail(c, err)
		return
	}
	views, err := s.proposalViews(c, project, []store.MCPProposal{created})
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, views[0])
}
