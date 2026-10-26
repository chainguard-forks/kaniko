/*
Copyright © 2022 Chris Mellard chris.mellard@icloud.com

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
package credhelper

import (
	"testing"

	"github.com/docker/docker-credential-helpers/credentials"
)

// TestIsACRHelper tests whether URLs are detected as being ACR/MCR registry
// hosts, to determine whether the cred helper should fail fast.
//
// Rows marked "Chainguard" were added with the GO-2026-6225 patch.
func TestIsACRHelper(t *testing.T) {
	for _, c := range []struct {
		url  string
		want bool
	}{
		{"myregistry.azurecr.io", true},
		{"myregistry.azurecr.cn", true},
		{"myregistry.azurecr.de", true},
		{"myregistry.azurecr.us", true},
		{"mcr.microsoft.com", true},
		{"myregistry.azurecr.me", false}, // not a known tld
		{"notacr.xcr.example", false},
		{"127.0.0.1:12345", false},
		{"localhost:12345", false},
		{"notaurl-)(*$@)(*@)(*", false},

		// Chainguard: legitimate hosts that must keep working.
		{"foo.azurecr.io:443", true},
		{"foo.azurecr.us:443", true},
		{"foo.azurecr.cn:5000", true},
		{"foo.westeurope.data.azurecr.io", true}, // dedicated data endpoint
		{"my-registry1.azurecr.io", true},
		{"mcr.microsoft.com:443", true},

		// Chainguard: GO-2026-6225 lookalikes.
		{"evil.azurecr.io.attacker.com", false},
		{"evil.azurecr.io.attacker.com:443", false},
		{"foo.azurecr.io.evil", false},
		{"foo.azurecr.us.attacker.com", false},
		{"mcr.microsoft.com.attacker.com", false},
		{"fooazurecr.io", false},
		{"azurecr.io", false},      // apex, no registry name
		{"foo.azurecr.io.", false}, // trailing dot

		// Chainguard: userinfo, path, query and fragment forms. url.Parse
		// splits these off before Host, so only the Host == input check
		// rejects them.
		{"foo.azurecr.io@attacker.com", false},
		{"attacker.com@foo.azurecr.io", false},
		{"user:pass@foo.azurecr.io", false},
		{"attacker.com/foo.azurecr.io", false},
		{"foo.azurecr.io/v2/", false},
		{"attacker.com?x=.azurecr.io", false},
		{"attacker.com#.azurecr.io", false},

		// Chainguard: percent-encoded hosts. Hostname() decodes these into
		// strings the old regex (and a naive anchored one) would match; the
		// Host == input check is what rejects them.
		{"[::1%25.azurecr.io]", false},
		{"foo%C3%A9.azurecr.io", false},

		// Chainguard: malformed labels.
		{"foo..azurecr.io", false},
		{".azurecr.io", false},
		{"-.azurecr.io", false},
		{"foo-.azurecr.io", false},
		{"-foo.azurecr.io", false},
		{"foo_bar.azurecr.io", false},
		{"foo!bar.azurecr.io", false},
		{"evil.com;foo.azurecr.io", false},
		{"fooé.azurecr.io", false},
		{"foo bar.azurecr.io", false},

		// Chainguard: odd ports.
		{"foo.azurecr.io:", false},
		{"foo.azurecr.io:0", false},
		{"foo.azurecr.io:65536", false},
		{"foo.azurecr.io:99999", false},
		{"foo.azurecr.io:123456", false},
		{"foo.azurecr.io:-1", false},
		{"foo.azurecr.io:443:443", false},
		{"foo.azurecr.io:abc", false},

		// Chainguard: matching stays case-sensitive, as upstream was.
		{"FOO.AZURECR.IO", false},
		{"foo.AzureCR.io", false},

		{"", false},
	} {
		t.Run(c.url, func(t *testing.T) {
			got := isACRRegistry(c.url)
			if got != c.want {
				t.Fatalf("got %t, want %t", got, c.want)
			}
		})
	}
}

// TestGetRejectsNonACRHostsAsNotFound checks that Get fails fast with a
// "credentials not found" error for non-ACR hosts, before any Azure token
// is requested. (Chainguard, GO-2026-6225.)
func TestGetRejectsNonACRHostsAsNotFound(t *testing.T) {
	for _, h := range []string{
		"evil.azurecr.io.attacker.com",
		"foo.azurecr.io.",
		"foo.azurecr.io@attacker.com",
		"index.docker.io",
	} {
		u, p, err := NewACRCredentialsHelper().Get(h)
		if !credentials.IsErrCredentialsNotFound(err) {
			t.Errorf("Get(%q): err = %v, want credentials not found", h, err)
		}
		if u != "" || p != "" {
			t.Errorf("Get(%q) returned credentials %q/%q, want none", h, u, p)
		}
	}
}
