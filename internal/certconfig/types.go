// Package certconfig defines certificate settings persisted through ConfigCenter.
package certconfig

import (
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
)

// Providers contains ACME, DNS and deployment credentials; it is never sent to browsers.
type Providers struct {
	ACME      ACME                `json:"acme"`
	DNS       map[string]DNS      `json:"dns"`
	Upstreams map[string]Upstream `json:"upstreams"`
}

// ACME selects the certificate authority and registration identity.
type ACME struct {
	DirectoryURL string `json:"directory_url"`
	Email        string `json:"email"`
	AcceptTerms  bool   `json:"accept_terms"`
}

// DNS selects an explicitly configured DNS provider.
type DNS struct {
	Kind      string                `json:"kind"`
	AccessKey string                `json:"access_key"`
	SecretKey string                `json:"secret_key"`
	Region    string                `json:"region"`
	APIBase   string                `json:"api_base"`
	Accounts  map[string]DNSAccount `json:"accounts"`
}

// DNSAccount is an acme-dns registration for a base name (without a wildcard).
type DNSAccount struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	Subdomain  string `json:"subdomain"`
	FullDomain string `json:"fulldomain"`
}

// Upstream configures cloud certificate storage or an SSH deployment.
type Upstream struct {
	Kind            string `json:"kind"`
	AccessKey       string `json:"access_key"`
	SecretKey       string `json:"secret_key"`
	Region          string `json:"region"`
	Address         string `json:"address"`
	User            string `json:"user"`
	PrivateKey      string `json:"private_key"`
	HostKey         string `json:"host_key"`
	CertificatePath string `json:"certificate_path"`
	KeyPath         string `json:"key_path"`
	ReloadCommand   string `json:"reload_command"`
}

// Domain is the ownership boundary and desired certificate policy.
type Domain struct {
	Name         string   `json:"name"`
	Owner        string   `json:"owner"`
	Certificates []Policy `json:"certificates"`
}

// Policy describes one independently issued certificate.
type Policy struct {
	ID              string   `json:"id"`
	Names           []string `json:"names"`
	DNSProvider     string   `json:"dns_provider"`
	Enabled         bool     `json:"enabled"`
	AutoRenew       bool     `json:"auto_renew"`
	RenewBeforeDays int      `json:"renew_before_days"`
	AutoDeploy      bool     `json:"auto_deploy"`
	Upstreams       []string `json:"upstreams"`
}

var label = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidDomain checks normalized ASCII DNS names, excluding IP addresses.
func ValidDomain(name string) bool {
	if len(name) > 253 || net.ParseIP(name) != nil || !strings.Contains(name, ".") {
		return false
	}
	for _, part := range strings.Split(name, ".") {
		if !label.MatchString(part) {
			return false
		}
	}
	return true
}

// Validate checks references and prevents issuing outside the managed domain.
func (d Domain) Validate(p Providers) error {
	if !ValidDomain(d.Name) {
		return errors.New("invalid domain: use lowercase ASCII or punycode")
	}
	if address, err := mail.ParseAddress(d.Owner); err != nil || address.Address != d.Owner {
		return errors.New("invalid domain owner email")
	}
	if len(d.Certificates) > 100 {
		return errors.New("at most 100 certificate policies are allowed")
	}
	seen := map[string]bool{}
	for _, c := range d.Certificates {
		if !label.MatchString(c.ID) || seen[c.ID] {
			return errors.New("certificate IDs must be unique DNS labels")
		}
		seen[c.ID] = true
		if len(c.Names) == 0 || len(c.Names) > 100 {
			return errors.New("a certificate needs 1 to 100 names")
		}
		if c.RenewBeforeDays < 1 || c.RenewBeforeDays > 60 {
			return errors.New("renew_before_days must be between 1 and 60")
		}
		names := map[string]bool{}
		for _, name := range c.Names {
			base := strings.TrimPrefix(name, "*.")
			if !ValidDomain(base) || names[name] || (base != d.Name && !strings.HasSuffix(base, "."+d.Name)) {
				return errors.New("certificate names must be unique and inside the managed domain")
			}
			names[name] = true
		}
		dns, ok := p.DNS[c.DNSProvider]
		if !ok {
			return fmt.Errorf("unknown DNS provider %q", c.DNSProvider)
		}
		if dns.Kind == "acme-dns" {
			for _, name := range c.Names {
				if _, ok := dns.Accounts[strings.TrimPrefix(name, "*.")]; !ok {
					return fmt.Errorf("missing acme-dns registration for %s", name)
				}
			}
		}
		targets := map[string]bool{}
		for _, target := range c.Upstreams {
			if _, ok := p.Upstreams[target]; !ok || targets[target] {
				return errors.New("invalid or duplicate upstream")
			}
			targets[target] = true
		}
		if c.AutoDeploy && len(c.Upstreams) == 0 {
			return errors.New("auto deployment needs an upstream")
		}
	}
	return nil
}

// Validate requires explicit credentials and HTTPS for credential-bearing services.
func (p Providers) Validate() error {
	if !httpsURL(p.ACME.DirectoryURL) {
		return errors.New("ACME directory_url must use HTTPS")
	}
	if _, err := mail.ParseAddress(p.ACME.Email); err != nil {
		return errors.New("ACME email is required")
	}
	for name, d := range p.DNS {
		if !label.MatchString(name) {
			return errors.New("invalid DNS provider name")
		}
		switch d.Kind {
		case "alidns", "tencentcloud":
			if d.AccessKey == "" || d.SecretKey == "" {
				return errors.New("DNS API credentials are required")
			}
		case "acme-dns":
			if !httpsURL(d.APIBase) {
				return errors.New("acme-dns api_base must use HTTPS")
			}
			for domain, a := range d.Accounts {
				if !ValidDomain(domain) || !ValidDomain(a.FullDomain) || a.Username == "" || a.Password == "" || a.Subdomain == "" {
					return errors.New("invalid acme-dns registration")
				}
			}
		default:
			return errors.New("unsupported DNS provider")
		}
	}
	for name, u := range p.Upstreams {
		if !label.MatchString(name) {
			return errors.New("invalid upstream name")
		}
		switch u.Kind {
		case "tencentcloud", "aliyun":
			if u.AccessKey == "" || u.SecretKey == "" {
				return errors.New("upstream API credentials are required")
			}
		case "ssh":
			if _, _, err := net.SplitHostPort(u.Address); err != nil {
				return errors.New("SSH address requires host:port")
			}
			if u.User == "" || u.PrivateKey == "" || u.HostKey == "" || !strings.HasPrefix(u.CertificatePath, "/") || !strings.HasPrefix(u.KeyPath, "/") || u.CertificatePath == u.KeyPath {
				return errors.New("SSH requires credentials, pinned host key and distinct absolute destination paths")
			}
		default:
			return errors.New("unsupported upstream")
		}
	}
	return nil
}

func httpsURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Fragment == ""
}
