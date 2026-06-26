package output

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/compute/metadata"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/warpstreamlabs/bento/public/service"
)

// gmkOAuthCache implements service.Cache and returns a Google Managed Kafka
// (GMK) OAUTHBEARER token from its Get() method. It is intended to be wired
// as the token_cache on a kafka output:
//
//	output:
//	  kafka:
//	    sasl:
//	      mechanism: OAUTHBEARER
//	      token_cache: gcp_managed_kafka_oauth_cache   # label of this resource
//
// IMPORTANT: GMK does NOT accept a bare OAuth2 access token over OAUTHBEARER.
// The broker expects a Google-specific token: three base64url segments joined
// with "." — a JWT-style header {"typ":"JWT","alg":"GOOG_OAUTH2_TOKEN"}, a
// payload {"exp","iss":"Google","iat","sub":<principal email>}, and the raw
// access token. Sending the plain access token yields:
//
//	kafka server: SASL Authentication failed: ... invalid credentials with
//	SASL mechanism OAUTHBEARER
//
// This format is defined by Google's reference implementation:
// https://github.com/googleapis/managedkafka/blob/main/kafka-auth-local-server/kafka_gcp_credentials_server.py
//
// oauth2.ReuseTokenSource caches the underlying access token until expiry, so
// we don't hit the metadata server on every broker connect. The token source is
// constructed once at startup — failures surface immediately rather than at
// first connect.
type gmkOAuthCache struct {
	src oauth2.TokenSource
	// principal is the service-account email used as the JWT `sub`. When empty
	// it is resolved lazily from the metadata server on first Get() call and
	// cached thereafter.
	principal string
}

// gmkJWTHeader is the fixed OAUTHBEARER header GMK requires. The alg value is
// a Google sentinel, not a real signing algorithm — the third segment carries
// the already-minted access token rather than a signature.
var gmkJWTHeader = gmkMustB64JSON(map[string]string{"typ": "JWT", "alg": "GOOG_OAUTH2_TOKEN"})

// gmkOAuthCacheSpec returns the configuration spec for the gcp_managed_kafka_oauth cache plugin.
func gmkOAuthCacheSpec() *service.ConfigSpec {
	return service.NewConfigSpec().
		Summary("Returns Google Managed Kafka (GMK) OAUTHBEARER tokens for use as a token_cache on the kafka output.").
		Description("Wraps Application Default Credentials (Workload Identity on GKE, gcloud locally) and " +
			"builds the Google-specific three-segment OAUTHBEARER token that GMK brokers require. " +
			"Wire this cache as the sasl.token_cache on a kafka output configured with mechanism OAUTHBEARER. " +
			"The Get() key is ignored — a fresh (or cached-until-expiry) token is returned on every call.").
		Field(service.NewStringListField("scopes").
			Description("OAuth2 scopes to request. Defaults to the cloud-platform scope required by GMK.").
			Default([]any{"https://www.googleapis.com/auth/cloud-platform"}))
}

// init registers the GMK OAUTHBEARER cache plugin with Bento. Because this file
// is in the output package, it runs automatically under the blank import
// _ "github.com/flexprice/bento-collector/output" already present in main.go.
func init() {
	err := service.RegisterCache(
		"gcp_managed_kafka_oauth",
		gmkOAuthCacheSpec(),
		func(conf *service.ParsedConfig, _ *service.Resources) (service.Cache, error) {
			scopes, err := conf.FieldStringList("scopes")
			if err != nil {
				return nil, fmt.Errorf("gcp_managed_kafka_oauth: read scopes: %w", err)
			}
			if len(scopes) == 0 {
				scopes = []string{"https://www.googleapis.com/auth/cloud-platform"}
			}

			// Construct the token source at startup so credential resolution
			// failures are visible immediately rather than on first broker connect.
			src, err := google.DefaultTokenSource(context.Background(), scopes...)
			if err != nil {
				return nil, fmt.Errorf("gcp_managed_kafka_oauth: resolve GCP default token source: %w", err)
			}

			// Allow an explicit principal override (matches the Google reference's
			// GOOGLE_MANAGED_KAFKA_AUTH_PRINCIPAL env var). Otherwise it is
			// resolved from the metadata server on first use.
			principal := os.Getenv("GOOGLE_MANAGED_KAFKA_AUTH_PRINCIPAL")

			return &gmkOAuthCache{
				src:       oauth2.ReuseTokenSource(nil, src),
				principal: principal,
			}, nil
		},
	)
	if err != nil {
		panic(err)
	}
}

// Get implements service.Cache. The key is ignored; a GMK OAUTHBEARER token
// is built from the current (or cached) GCP access token and returned as bytes.
func (c *gmkOAuthCache) Get(ctx context.Context, _ string) ([]byte, error) {
	tok, err := c.src.Token()
	if err != nil {
		return nil, fmt.Errorf("gcp_managed_kafka_oauth: fetch GCP access token: %w", err)
	}

	principal, err := c.resolvePrincipal()
	if err != nil {
		return nil, fmt.Errorf("gcp_managed_kafka_oauth: resolve principal email: %w", err)
	}

	payload := gmkMustB64JSON(map[string]any{
		"exp": tok.Expiry.UTC().Unix(),
		"iss": "Google",
		"iat": time.Now().UTC().Unix(),
		"sub": principal,
	})
	encodedToken := base64.RawURLEncoding.EncodeToString([]byte(tok.AccessToken))

	gmkToken := strings.Join([]string{gmkJWTHeader, payload, encodedToken}, ".")
	return []byte(gmkToken), nil
}

// Set implements service.Cache — no-op; tokens are not externally settable.
func (c *gmkOAuthCache) Set(_ context.Context, _ string, _ []byte, _ *time.Duration) error {
	return nil
}

// Add implements service.Cache — no-op; tokens are not externally settable.
func (c *gmkOAuthCache) Add(_ context.Context, _ string, _ []byte, _ *time.Duration) error {
	return nil
}

// Delete implements service.Cache — no-op; tokens are not externally deletable.
func (c *gmkOAuthCache) Delete(_ context.Context, _ string) error {
	return nil
}

// Close implements service.Cache — no resources to release.
func (c *gmkOAuthCache) Close(_ context.Context) error {
	return nil
}

// resolvePrincipal returns the service-account email for the JWT `sub`. It is
// cached after the first successful lookup so the metadata server is not hit on
// every token build.
func (c *gmkOAuthCache) resolvePrincipal() (string, error) {
	if c.principal != "" {
		return c.principal, nil
	}
	// Under Workload Identity the access token does not carry the SA email, so
	// fetch it from the metadata server (the impersonated GSA's default SA).
	email, err := metadata.Email("default")
	if err != nil {
		return "", fmt.Errorf("query metadata server for SA email (set GOOGLE_MANAGED_KAFKA_AUTH_PRINCIPAL to override): %w", err)
	}
	c.principal = email
	return email, nil
}

// gmkMustB64JSON marshals v to JSON and base64url-encodes it (no padding).
// Panics on marshal failure — inputs are always fixed maps of primitives.
func gmkMustB64JSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		// The inputs are fixed maps of primitives; marshaling cannot fail.
		panic(fmt.Sprintf("gcp_managed_kafka_oauth: marshal token segment: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
