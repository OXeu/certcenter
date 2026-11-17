package certconfig

import (
	"errors"
	"net"
	"strings"
)

// ManagedDNSProvider is the built-in CNAME validation provider.
const ManagedDNSProvider = "certcenter-acme-dns"

// ACMEDNS configures the authoritative DNS listener for delegated challenges.
type ACMEDNS struct {
	Zone          string   `json:"zone"`
	ListenAddress string   `json:"listen_address"`
	Nameserver    string   `json:"nameserver"`
	Addresses     []string `json:"addresses"`
}

// Validate checks the public authority and its DNS listener; an empty zone disables it.
func (d ACMEDNS) Validate() error {
	if d.Zone == "" {
		return nil
	}
	if !ValidDomain(d.Zone) || len(d.Zone) > 220 || !ValidDomain(d.Nameserver) {
		return errors.New("acme_dns requires a valid zone and nameserver")
	}
	if _, port, err := net.SplitHostPort(d.ListenAddress); err != nil || port == "" {
		return errors.New("acme_dns requires listen_address host:port")
	}
	if len(d.Addresses) == 0 {
		return errors.New("acme_dns requires nameserver IP addresses")
	}
	for _, address := range d.Addresses {
		if net.ParseIP(address) == nil || strings.Contains(address, "%") {
			return errors.New("acme_dns addresses must be IPv4 or IPv6 addresses")
		}
	}
	return nil
}
