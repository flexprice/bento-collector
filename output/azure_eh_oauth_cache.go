package output

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/warpstreamlabs/bento/public/service"
)

// azureEHOAuthCache implements service.Cache and returns an Azure AD access
// token from its Get() method, for use as the token_cache on a kafka input or
// output that talks to Azure Event Hubs over the Kafka protocol with
// mechanism OAUTHBEARER:
//
//	input:
//	  kafka:
//	    sasl:
//	      mechanism: OAUTHBEARER
//	      token_cache: azure_eventhub_oauth   # label of this resource
//	      token_key: token
//
// Unlike GMK (see gmk_oauth_cache.go), Azure Event Hubs accepts the RAW AAD
// access token as the OAUTHBEARER bearer — no JWT wrapping, no principal
// segment. The token is a bearer for the Event Hubs resource, scope
// "https://<namespace>.servicebus.windows.net/.default".
//
// Credential resolution:
//   - When AZURE_EH_MANAGED_IDENTITY_CLIENT_ID is set, a User-Assigned Managed
//     Identity (UAMI) with that client ID is used. This is the intended path
//     when the collector runs on Azure compute (AKS / Container App / VM) with
//     the UAMI attached and granted the "Azure Event Hubs Data Receiver" role
//     on the namespace or entity.
//   - When unset, DefaultAzureCredential is used (system-assigned MI, env-var
//     service principal, Azure CLI for local dev, etc.).
//
// azcore.AccessToken carries an ExpiresOn; azidentity credentials cache the
// underlying token internally and only refresh when it is near expiry, so
// calling GetToken on every broker connect does not hit AAD each time.
type azureEHOAuthCache struct {
	cred   azcore.TokenCredential
	scopes []string
}

// azureEHOAuthCacheSpec returns the configuration spec for the
// azure_eventhub_oauth cache plugin.
func azureEHOAuthCacheSpec() *service.ConfigSpec {
	return service.NewConfigSpec().
		Summary("Returns Azure AD OAUTHBEARER tokens for authenticating to Azure Event Hubs over the Kafka protocol.").
		Description("Mints an Azure AD access token scoped to the Event Hubs namespace and returns it as the OAUTHBEARER bearer. " +
			"Wire this cache as the sasl.token_cache on a kafka input/output configured with mechanism OAUTHBEARER and token_key: token. " +
			"The Get() key is ignored — a fresh (or internally-cached-until-expiry) token is returned on every call. " +
			"On Azure compute with a User-Assigned Managed Identity, set AZURE_EH_MANAGED_IDENTITY_CLIENT_ID to the UAMI client ID and " +
			"grant that identity the 'Azure Event Hubs Data Receiver' role on the namespace or entity — no connection string or SAS key needed. " +
			"When AZURE_EH_MANAGED_IDENTITY_CLIENT_ID is unset, DefaultAzureCredential is used (system-assigned MI, env service principal, or Azure CLI for local dev).").
		Field(service.NewStringField("scope").
			Description("AAD scope to request the token for. Must match the Event Hubs namespace, e.g. https://<namespace>.servicebus.windows.net/.default. " +
				"May also be set via the AZURE_EH_OAUTH_SCOPE env var; this field takes precedence when non-empty.").
			Default(""))
}

func init() {
	err := service.RegisterCache(
		"azure_eventhub_oauth",
		azureEHOAuthCacheSpec(),
		func(conf *service.ParsedConfig, _ *service.Resources) (service.Cache, error) {
			scope, err := conf.FieldString("scope")
			if err != nil {
				return nil, fmt.Errorf("azure_eventhub_oauth: read scope: %w", err)
			}
			if scope == "" {
				scope = os.Getenv("AZURE_EH_OAUTH_SCOPE")
			}
			if scope == "" {
				return nil, fmt.Errorf("azure_eventhub_oauth: scope is required — set the `scope` field or AZURE_EH_OAUTH_SCOPE " +
					"to https://<namespace>.servicebus.windows.net/.default")
			}

			// Construct the credential at startup so resolution failures are
			// visible immediately rather than on first broker connect.
			var cred azcore.TokenCredential
			if clientID := os.Getenv("AZURE_EH_MANAGED_IDENTITY_CLIENT_ID"); clientID != "" {
				cred, err = azidentity.NewManagedIdentityCredential(&azidentity.ManagedIdentityCredentialOptions{
					ID: azidentity.ClientID(clientID),
				})
				if err != nil {
					return nil, fmt.Errorf("azure_eventhub_oauth: build user-assigned managed identity credential: %w", err)
				}
			} else {
				cred, err = azidentity.NewDefaultAzureCredential(nil)
				if err != nil {
					return nil, fmt.Errorf("azure_eventhub_oauth: build default azure credential: %w", err)
				}
			}

			return &azureEHOAuthCache{
				cred:   cred,
				scopes: []string{scope},
			}, nil
		},
	)
	if err != nil {
		panic(err)
	}
}

// Get implements service.Cache. The key is ignored; a fresh (or
// internally-cached-until-expiry) Azure AD access token is returned as bytes.
func (c *azureEHOAuthCache) Get(ctx context.Context, _ string) ([]byte, error) {
	tok, err := c.cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: c.scopes})
	if err != nil {
		return nil, fmt.Errorf("azure_eventhub_oauth: fetch Azure AD access token: %w", err)
	}
	return []byte(tok.Token), nil
}

// Set implements service.Cache — no-op; tokens are not externally settable.
func (c *azureEHOAuthCache) Set(_ context.Context, _ string, _ []byte, _ *time.Duration) error {
	return nil
}

// Add implements service.Cache — no-op; tokens are not externally settable.
func (c *azureEHOAuthCache) Add(_ context.Context, _ string, _ []byte, _ *time.Duration) error {
	return nil
}

// Delete implements service.Cache — no-op; tokens are not externally deletable.
func (c *azureEHOAuthCache) Delete(_ context.Context, _ string) error {
	return nil
}

// Close implements service.Cache — no resources to release.
func (c *azureEHOAuthCache) Close(_ context.Context) error {
	return nil
}
