package store

import "slices"

const (
	DomainsRead        Permission = "domains:read"
	DomainsWrite       Permission = "domains:write"
	CertificatesIssue  Permission = "certificates:issue"
	CertificatesDeploy Permission = "certificates:deploy"
	ProvidersRead      Permission = "providers:read"
	ProvidersWrite     Permission = "providers:write"
)

// CertificatePermissions returns the operations available to certificate API keys.
func CertificatePermissions() []Permission {
	return []Permission{DomainsRead, DomainsWrite, CertificatesIssue, CertificatesDeploy, ProvidersRead, ProvidersWrite, KeysRead, KeysWrite, UsersRead, UsersWrite, UsersDelete, SettingsRead, SettingsWrite}
}

// AllowsDomain checks a certificate operation within a domain scope.
func (k APIKey) AllowsDomain(domain string, permission Permission) bool {
	for _, scope := range k.Scopes {
		if scope.Domain != "" && (scope.Domain == "*" || (!permission.GlobalOnly() && scope.Domain == domain)) && slices.Contains(scope.Permissions, permission) {
			return true
		}
	}
	return false
}
