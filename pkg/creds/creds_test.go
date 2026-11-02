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
)

// patchedACRHelper is the import path of the patched in-tree ACR helper
// (GO-2026-6225). See third_party/docker-credential-acr-env/PATCHES.md.
const patchedACRHelper = "github.com/chainguard-dev/kaniko/third_party/docker-credential-acr-env/pkg/credhelper"

// TestGetKeychainUsesPatchedACRHelper fails if creds.go stops importing the
// patched in-tree ACR helper (for example if it goes back to
// github.com/chrismellard/docker-credential-acr-env). It is a cheap check on
// the wiring; TestGetKeychainRequestsAzureTokenOnlyForACRHosts checks the
// behaviour through GetKeychain, and also catches an unpatched helper wired in
// under a different import path.
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
