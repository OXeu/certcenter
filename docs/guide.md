# CertCenter setup guide

CertCenter manages domains, ACME certificate policies, issuance, renewal and upstream deployment. Its Go binary embeds the Vue frontend. Use the root README for build commands and the initial local start.

## Local mode

Without ConfigCenter connection parameters, the service initializes persistent settings in `.tmp/certcenter-config.kv` and application data in `.tmp/certcenter.db`. It generates an encryption key when initializing settings. Preserve this key and the settings store alongside application data. The default listener and public URL are `127.0.0.1:8081` and `http://127.0.0.1:8081`.

The default OAuth provider is empty, so the first local launch serves the login page without enabling sign-in. Use the remote configuration flow below to prepare a usable identity provider and certificate settings.

## Bootstrap configuration

Only these bootstrap values are read from the environment or optional `.env.local`:

| Variable                      | Purpose / default                                               |
| ----------------------------- | --------------------------------------------------------------- |
| `CC_ENV_FILE`                 | Alternate bootstrap file; defaults to `.env.local`              |
| `CC_CONFIGCENTER_URL`         | ConfigCenter base URL; omit both URL and API key for local mode |
| `CC_CONFIGCENTER_API_KEY`     | Project-scoped ConfigCenter key                                 |
| `CC_CONFIGCENTER_ENVIRONMENT` | Environment; defaults to `production`                           |
| `CC_CONFIGCENTER_LOCAL_PATH`  | Local settings file; defaults to `.tmp/certcenter-config.kv`    |

Server settings such as the listener, database, OAuth provider and encryption key are stored settings, not arbitrary `CC_ADDR` or `CC_DB` environment overrides.

## Configure a usable service

1. Create a dedicated ConfigCenter project and a key permitted to read configuration, create missing defaults and write configuration. Supply the bootstrap URL, key and environment.
2. Start CertCenter once. It initializes missing `certcenter.server` and `certcenter.providers` settings without replacing existing values. Stop the service while preparing its first production configuration.
3. In ConfigCenter, edit `certcenter.server`: set `public_url`, `addr`, database settings, `oauth.issuer_url`, `oauth.client_id`, `oauth.client_secret`, `oauth.redirect_url`, and `bootstrap.admin_email` for the intended verified identity. Keep the generated `encryption_key`. The full shapes are defined in [server configuration](../internal/config/config.go) and [OAuth configuration](../internal/auth/auth.go).
4. Register the public URL plus `/oauth/callback` at the identity provider. For the first production run, point `database_dsn` at a new, empty application database before restarting: the first initialization above used the default administrator identity, and changing bootstrap settings does not rewrite existing users. The empty production database will initialize the administrator specified in the prepared configuration.

Choose AuthCenter's `oauth.scopes` in `certcenter.server` according to its signing configuration:

- With `AC_OIDC_SIGNING_KEYS` configured in AuthCenter, use `["openid", "profile", "email"]` to receive signed ID Tokens verified through its published JWKS.
- Without signing keys, use `["profile", "email"]`; the shared client omits unsupported `openid` and `nonce`, preserves PKCE, and reads identity from userinfo when no ID Token is issued.

AuthCenter does not support the `groups` scope.
5. Configure the ACME account, DNS providers, domains, certificate policies and upstream deployment targets. Provider settings are stored under `certcenter.providers`; domain configuration and jobs are managed by the application.

## Production and shared builds

Use an HTTPS public address, persistent settings and a database appropriate for the deployment. Keep encrypted certificate material, provider credentials and the encryption key in their configured stores. The GitHub workflow provides Tencent SCF packaging and deployment hooks; deployment-specific credentials stay in the consumer repository's environment and secrets.

`common.lock.json` pins the common source and CI actions. Use `make common-local COMMON_DIR=../center-common` for local development and `CI=true make bootstrap check build` for a pinned build. Run `make check` and `make build` before upgrading the common revision. Rollback restores the previous lockfile and matching adaptations.
