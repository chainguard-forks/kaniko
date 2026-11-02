/*
Copyright 2026 Google LLC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package creds

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
)

// The fake Azure cloud and service principal. The helper asks for tokens for
// fakeAzureResourceManager; nothing ever connects to it.
const (
	fakeAzureResourceManager = "https://management.kaniko-unit-test.invalid/"
	fakeAzureTenant          = "kaniko-unit-test-tenant"
	fakeAzureClient          = "kaniko-unit-test-client"
)

// fakeTokenPath is where adal sends a tenant's token requests, relative to the
// cloud's activeDirectoryEndpoint.
const fakeTokenPath = "/" + fakeAzureTenant + "/oauth2/token"

// tokenRequest is one request the fake Azure AD endpoint received.
type tokenRequest struct {
	method, path string
	form         url.Values
}

// fakeAzureAD points the ACR helper at a local Azure AD token endpoint and
// gives it service principal credentials, with an empty Docker config. It
// returns a function that returns the requests received since its last call
// and forgets them.
//
// The endpoint answers every request with HTTP 400, so the helper fails right
// after asking for a token and never goes on to the registry's
// /oauth2/exchange. The tests therefore make no network calls, even for real
// ACR hosts.
//
// The helper finds the endpoint through go-autorest's AZURESTACKCLOUD
// environment, which reads the cloud's endpoints from
// AZURE_ENVIRONMENT_FILEPATH. A replacement helper that doesn't use
// go-autorest won't see it (see third_party/docker-credential-acr-env/PATCHES.md).
func fakeAzureAD(t *testing.T) func() []tokenRequest {
	t.Helper()

	var mu sync.Mutex
	var requests []tokenRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		requests = append(requests, tokenRequest{r.Method, r.URL.Path, r.PostForm})
		mu.Unlock()
		http.Error(w, `{"error":"invalid_client"}`, http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	cloud, err := json.Marshal(map[string]string{
		"name":                    "AzureStackCloud",
		"activeDirectoryEndpoint": srv.URL + "/",
		"resourceManagerEndpoint": fakeAzureResourceManager,
	})
	if err != nil {
		t.Fatal(err)
	}
	cloudFile := filepath.Join(dir, "cloud.json")
	if err := os.WriteFile(cloudFile, cloud, 0o600); err != nil {
		t.Fatal(err)
	}

	// No Docker or Podman credentials, so authn.DefaultKeychain answers
	// Anonymous and GetKeychain falls through to the ACR helper.
	for k, v := range map[string]string{
		"HOME":               dir,
		"DOCKER_CONFIG":      dir,
		"XDG_CONFIG_HOME":    dir,
		"XDG_RUNTIME_DIR":    dir,
		"REGISTRY_AUTH_FILE": "",
	} {
		t.Setenv(k, v)
	}
	// Service principal credentials for the fake cloud. The helper tries
	// these before any other Azure auth method, so other Azure settings in
	// the environment don't change which endpoint it calls.
	// AZURE_AD_RESOURCE would change the resource it asks for.
	for k, v := range map[string]string{
		"AZURE_ENVIRONMENT":          "AZURESTACKCLOUD",
		"AZURE_ENVIRONMENT_FILEPATH": cloudFile,
		"AZURE_TENANT_ID":            fakeAzureTenant,
		"AZURE_CLIENT_ID":            fakeAzureClient,
		"AZURE_CLIENT_SECRET":        "kaniko-unit-test-secret",
		"AZURE_AD_RESOURCE":          "",
	} {
		t.Setenv(k, v)
	}

	return func() []tokenRequest {
		mu.Lock()
		defer mu.Unlock()
		got := requests
		requests = nil
		return got
	}
}

// TestGetKeychainRequestsAzureTokenOnlyForACRHosts resolves credentials for
// image references through GetKeychain and checks which ones make the ACR
// helper ask Azure AD for a token (GO-2026-6225). Lookalike hosts must not:
// before the fix, any host containing ".azurecr.io" got an Azure Resource
// Manager token. The rows for real ACR and MCR hosts check that the harness
// does see token requests, so the lookalike rows can't pass just because the
// helper was never reached.
//
// Each row is resolved the two ways kaniko does: for a repository, as
// go-containerregistry's remote package does for FROM and COPY --from images,
// the cache and the push permission check (pkg/image/remote, pkg/cache,
// pkg/executor/push.go), and for a registry, as pkg/executor/push.go does
// when it builds the push authenticator.
func TestGetKeychainRequestsAzureTokenOnlyForACRHosts(t *testing.T) {
	takeRequests := fakeAzureAD(t)

	resolvers := []struct {
		name    string
		resolve func(context.Context, name.Repository) (authn.Authenticator, error)
	}{
		{"repository", func(ctx context.Context, repo name.Repository) (authn.Authenticator, error) {
			return authn.Resolve(ctx, GetKeychain(), repo)
		}},
		{"registry", func(_ context.Context, repo name.Repository) (authn.Authenticator, error) {
			return GetKeychain().Resolve(repo.Registry)
		}},
	}
	for _, tc := range []struct {
		name             string
		ref              string
		wantTokenRequest bool
	}{
		{"requests token for ACR registry", "foo.azurecr.io/app", true},
		{"requests token for ACR registry with port", "foo.azurecr.io:443/app", true},
		{"requests token for ACR data endpoint", "foo.westeurope.data.azurecr.io/app", true},
		{"requests token for MCR", "mcr.microsoft.com/dotnet/runtime", true},
		{"no token for azurecr.io lookalike", "x.azurecr.io.attacker.example/app", false},
		{"no token for azurecr.io lookalike with port", "x.azurecr.io.attacker.example:443/app", false},
		{"no token for azurecr.us lookalike", "x.azurecr.us.attacker.example/app", false},
		{"no token for azurecr.cn lookalike", "x.azurecr.cn.attacker.example/app", false},
		{"no token for azurecr.io without a dot before it", "evilazurecr.io/app", false},
		{"no token for unknown azurecr TLD", "foo.azurecr.me/app", false},
		{"no token for MCR lookalike", "mcr.microsoft.com.attacker.example/app", false},
		{"no token for unrelated registry", "registry.attacker.example/app", false},
	} {
		for _, r := range resolvers {
			t.Run(tc.name+"/"+r.name, func(t *testing.T) {
				takeRequests() // drop anything left over from an earlier row

				// Strict validation rejects a ref without an explicit
				// registry, which would otherwise be parsed as a Docker Hub
				// repository and never reach the ACR helper.
				repo, err := name.NewRepository(tc.ref, name.StrictValidation)
				if err != nil {
					t.Fatalf("name.NewRepository(%q): %v", tc.ref, err)
				}
				auth, err := r.resolve(t.Context(), repo)
				if err != nil {
					t.Fatalf("resolving %q by %s: %v", tc.ref, r.name, err)
				}

				got := takeRequests()
				if gotTokenRequest := len(got) > 0; gotTokenRequest != tc.wantTokenRequest {
					t.Fatalf("resolving %q by %s: Azure AD token requested = %t, want %t; requests: %+v", tc.ref, r.name, gotTokenRequest, tc.wantTokenRequest, got)
				}
				for _, req := range got {
					if req.method != http.MethodPost || req.path != fakeTokenPath ||
						req.form.Get("grant_type") != "client_credentials" ||
						req.form.Get("client_id") != fakeAzureClient ||
						req.form.Get("resource") != fakeAzureResourceManager {
						t.Errorf("resolving %q by %s: got request %+v, want a client_credentials POST to %s for client %q and resource %q",
							tc.ref, r.name, req, fakeTokenPath, fakeAzureClient, fakeAzureResourceManager)
					}
				}
				// Every row ends up anonymous: the ACR helper either rejects
				// the host or gets the fake endpoint's 400, and
				// go-containerregistry turns any helper error into Anonymous,
				// so GetKeychain falls through to the next keychain. This
				// pins that fall-through. It can't detect the GO-2026-6225
				// leak itself, which also ends in Anonymous; the token
				// request check above does that.
				if auth != authn.Anonymous {
					t.Errorf("resolving %q by %s: got credentials %v, want authn.Anonymous", tc.ref, r.name, auth)
				}
			})
		}
	}
}
