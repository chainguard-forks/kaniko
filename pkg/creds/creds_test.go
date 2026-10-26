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
	"go/parser"
	"go/token"
	"strconv"
	"testing"

	"github.com/chainguard-dev/kaniko/third_party/docker-credential-acr-env/pkg/credhelper"
	"github.com/docker/docker-credential-helpers/credentials"
)

// patchedACRHelper is the import path of the patched in-tree ACR helper
// (GO-2026-6225). See third_party/docker-credential-acr-env/PATCHES.md.
const patchedACRHelper = "github.com/chainguard-dev/kaniko/third_party/docker-credential-acr-env/pkg/credhelper"

// isolateACRHelper makes sure the ACR helper can never reach the network in
// these tests, even if an unpatched helper were linked instead. The unpatched
// upstream helper accepts lookalike hosts and goes on to read its Azure
// settings; an invalid AZURE_ENVIRONMENT makes that fail before any token
// request, so the test fails with a non "not found" error rather than calling
// Azure. The patched helper rejects the host before reading any settings.
func isolateACRHelper(t *testing.T) {
	t.Helper()
	t.Setenv("AZURE_ENVIRONMENT", "kaniko-unit-test-no-such-cloud")
	for _, v := range []string{"AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET", "AZURE_TENANT_ID", "AZURE_FEDERATED_TOKEN", "AZURE_FEDERATED_TOKEN_FILE"} {
		t.Setenv(v, "")
	}
}

// TestGetKeychainUsesPatchedACRHelper fails if creds.go stops importing the
// patched in-tree ACR helper (for example if it goes back to
// github.com/chrismellard/docker-credential-acr-env). The wiring is checked
// by reading creds.go's imports because a behavioural check through
// GetKeychain cannot tell the helpers apart: authn.NewKeychainFromHelper turns
// any helper error into Anonymous.
func TestGetKeychainUsesPatchedACRHelper(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "creds.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case path == patchedACRHelper:
			found = true
		case path == "github.com/chrismellard/docker-credential-acr-env/pkg/credhelper":
			t.Errorf("creds.go imports the unpatched upstream ACR helper %q (GO-2026-6225)", path)
		}
	}
	if !found {
		t.Errorf("creds.go does not import the patched ACR helper %q", patchedACRHelper)
	}
}

// TestACRHelperIsPatched checks the in-tree helper rejects lookalike hosts
// with "credentials not found" before any token request.
func TestACRHelperIsPatched(t *testing.T) {
	isolateACRHelper(t)

	for _, h := range []string{"evil.azurecr.io.attacker.com", "foo.azurecr.io.", "foo.azurecr.io@attacker.com"} {
		_, _, err := credhelper.NewACRCredentialsHelper().Get(h)
		if !credentials.IsErrCredentialsNotFound(err) {
			t.Errorf("ACR helper Get(%q): err = %v, want credentials not found", h, err)
		}
	}
}
