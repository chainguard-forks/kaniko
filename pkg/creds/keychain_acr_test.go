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
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
)

// fakeAzureResourceManager is the Resource Manager endpoint of the fake Azure
// cloud. The helper asks for tokens with this as the resource; nothing ever
// connects to it.
const fakeAzureResourceManager = "https://management.kaniko-unit-test.invalid/"

// fakeAzureAD points the ACR helper at a local Azure AD token endpoint and
// gives it service principal credentials, with an empty Docker config. It
// returns a function that reports the form bodies of the token requests
// received so far.
//
// The endpoint answers every request with HTTP 400, so the helper fails right
// after asking for a token and never goes on to the registry's
// /oauth2/exchange. The tests therefore make no network calls, even for real
// ACR hosts.
func fakeAzureAD(t *testing.T) func() []url.Values {
	t.Helper()

	var mu sync.Mutex
	var requests []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		mu.Lock()
		requests = append(requests, form)
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
		"AZURE_TENANT_ID":            "kaniko-unit-test-tenant",
		"AZURE_CLIENT_ID":            "kaniko-unit-test-client",
		"AZURE_CLIENT_SECRET":        "kaniko-unit-test-secret",
		"AZURE_AD_RESOURCE":          "",
	} {
		t.Setenv(k, v)
	}

	return func() []url.Values {
		mu.Lock()
		defer mu.Unlock()
		return append([]url.Values(nil), requests...)
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
// go-containerregistry's remote package does for FROM and COPY --from images
// and the cache (pkg/image/remote, pkg/cache), and for a registry, as
// pkg/executor/push.go does for the destination.
func TestGetKeychainRequestsAzureTokenOnlyForACRHosts(t *testing.T) {
	resolvers := []struct {
		name    string
		resolve func(name.Repository) (authn.Authenticator, error)
	}{
		{"repository", func(repo name.Repository) (authn.Authenticator, error) {
			return authn.Resolve(t.Context(), GetKeychain(), repo)
		}},
		{"registry", func(repo name.Repository) (authn.Authenticator, error) {
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
				tokenRequests := fakeAzureAD(t)

				repo, err := name.NewRepository(tc.ref)
				if err != nil {
					t.Fatalf("name.NewRepository(%q): %v", tc.ref, err)
				}
				// A ref without a registry host would be parsed as a
				// Docker Hub repository and never reach the ACR helper.
				if host, _, _ := strings.Cut(tc.ref, "/"); repo.RegistryStr() != host {
					t.Fatalf("name.NewRepository(%q): registry = %q, want %q", tc.ref, repo.RegistryStr(), host)
				}
				auth, err := r.resolve(repo)
				if err != nil {
					t.Fatalf("resolving %q by %s: %v", tc.ref, r.name, err)
				}

				got := tokenRequests()
				if gotTokenRequest := len(got) > 0; gotTokenRequest != tc.wantTokenRequest {
					t.Fatalf("resolving %q by %s: Azure AD token requested = %t, want %t; requests: %v", tc.ref, r.name, gotTokenRequest, tc.wantTokenRequest, got)
				}
				for _, form := range got {
					if res := form.Get("resource"); res != fakeAzureResourceManager {
						t.Errorf("resolving %q by %s: token requested for resource %q, want %q", tc.ref, r.name, res, fakeAzureResourceManager)
					}
				}
				if !tc.wantTokenRequest && auth != authn.Anonymous {
					t.Errorf("resolving %q by %s: got credentials %v, want authn.Anonymous", tc.ref, r.name, auth)
				}
			})
		}
	}
}
