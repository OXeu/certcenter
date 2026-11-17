package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/OXeu/certcenter/internal/acmedns"
	"github.com/OXeu/certcenter/internal/certconfig"
	"github.com/gin-gonic/gin"
)

func (s *Server) domainDelegations(ctx context.Context, domain certconfig.Domain, providers certconfig.Providers) ([]acmedns.Record, error) {
	records := []acmedns.Record{}
	seen := map[string]bool{}
	add := func(record acmedns.Record) {
		key := record.Name + "\x00" + record.Target
		if !seen[key] {
			seen[key] = true
			records = append(records, record)
		}
	}
	if len(domain.Certificates) == 0 && s.delegatedDNS != nil {
		record, err := s.delegatedDNS.Ensure(ctx, domain.Name, domain.Name)
		if err != nil {
			return nil, fmt.Errorf("prepare CNAME delegation: %w", err)
		}
		add(record)
	}
	for _, policy := range domain.Certificates {
		provider := providers.DNS[policy.DNSProvider]
		if provider.Kind != "acme-dns" {
			continue
		}
		for _, name := range policy.Names {
			base := strings.TrimPrefix(name, "*.")
			if provider.APIBase != "" {
				if account, ok := provider.Accounts[base]; ok {
					add(acmedns.Record{Name: "_acme-challenge." + base, Target: account.FullDomain})
				}
			} else if s.delegatedDNS != nil {
				record, err := s.delegatedDNS.Ensure(ctx, domain.Name, base)
				if err != nil {
					return nil, fmt.Errorf("prepare CNAME delegation: %w", err)
				}
				add(record)
			}
		}
	}
	return records, nil
}

func (s *Server) checkDomainDelegations(c *gin.Context) {
	domain, ok := s.authorizeDomain(c)
	if !ok {
		return
	}
	providers, err := s.cfg.Remote.Providers(c.Request.Context())
	if err != nil {
		s.certificateError(c, err)
		return
	}
	records, err := s.domainDelegations(c.Request.Context(), domain, providers)
	if err != nil {
		s.certificateError(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	for i := range records {
		target, err := net.DefaultResolver.LookupCNAME(ctx, records[i].Name)
		verified := err == nil && strings.EqualFold(strings.TrimSuffix(target, "."), strings.TrimSuffix(records[i].Target, "."))
		records[i].Verified = &verified
	}
	c.JSON(http.StatusOK, gin.H{"delegations": records})
}
