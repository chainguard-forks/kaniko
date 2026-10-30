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
	"bytes"
	"strings"
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
		{"FOO.azurecr.io", false},

		// Chainguard: MCR must match exactly, not as a suffix.
		{"foo.mcr.microsoft.com", false},
		{"evilmcr.microsoft.com", false},

		// Chainguard: bracketed names. net/url only accepts an IPv6 address
		// inside brackets (Go 1.24.8 and 1.25.2 onwards), so these fail to
		// parse. With an older toolchain they parse, Hostname() strips the
		// brackets, and the result matches acrHostRE; these rows catch that.
		{"[foo.azurecr.io]", false},
		{"[foo.azurecr.io]:443", false},
		{"[mcr.microsoft.com]", false},

		// Chainguard: IPv6 literals. These parse; the hostname check
		// rejects them.
		{"[::1]", false},
		{"[::1]:443", false},
		{"[fe80::1%25foo.azurecr.io]", false},

		// Chainguard: whitespace, control characters, backslashes and
		// scheme prefixes. url.Parse rejects most of these before
		// isACRRegistry's own checks run.
		{"foo.azurecr.io\n", false},
		{"foo.azurecr.io\t", false},
		{" foo.azurecr.io", false},
		{"foo.azurecr.io\x00.attacker.com", false},
		{"foo\\.azurecr.io", false},
		{"foo.azurecr.io\\@attacker.com", false},
		{"//foo.azurecr.io", false},
		{"https://foo.azurecr.io", false},

		// Chainguard: percent-encoded dots.
		{"foo.azurecr.io%2eattacker.com", false},
		{"foo.azurecr%2eio", false},

		// Chainguard: label length and punycode.
		{"a23456789012345678901234567890123456789012345678901234567890123.azurecr.io", true},   // 63-char label
		{"a234567890123456789012345678901234567890123456789012345678901234.azurecr.io", false}, // 64-char label
		{"xn--e1afmkfd.azurecr.io", true},

		{"", false},
	} {
		t.Run(c.url, func(t *testing.T) {
			got := isACRRegistry(c.url)
			if got != c.want {
				t.Fatalf("isACRRegistry(%q) = %t, want %t", c.url, got, c.want)
			}
		})
	}
}

// isolateAzure makes any attempt by the helper to get an Azure token fail
// while it reads its settings, before it contacts Azure AD or the instance
// metadata service. Without this, a helper that let a non-ACR host through
// would fall back to the managed identity path and call IMDS. (Chainguard,
// GO-2026-6225.)
func isolateAzure(t *testing.T) {
	t.Helper()
	t.Setenv("AZURE_ENVIRONMENT", "kaniko-unit-test-no-such-cloud")
	for _, v := range []string{
		"AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET", "AZURE_TENANT_ID",
		"AZURE_FEDERATED_TOKEN", "AZURE_FEDERATED_TOKEN_FILE",
		"MSI_ENDPOINT", "MSI_SECRET",
	} {
		t.Setenv(v, "")
	}
}

// TestGetRejectsNonACRHostsAsNotFound checks that Get fails fast with a
// "credentials not found" error for non-ACR hosts, before any Azure token
// is requested. The ACR row checks that a real ACR host gets past the host
// check and fails later, so the other rows can't pass just because every
// host fails. (Chainguard, GO-2026-6225.)
func TestGetRejectsNonACRHostsAsNotFound(t *testing.T) {
	isolateAzure(t)
	for _, tc := range []struct {
		name         string
		serverURL    string
		wantNotFound bool
	}{
		{"passes ACR registry to token request", "foo.azurecr.io", false},
		{"rejects azurecr.io lookalike", "evil.azurecr.io.attacker.com", true},
		{"rejects trailing dot", "foo.azurecr.io.", true},
		{"rejects userinfo", "foo.azurecr.io@attacker.com", true},
		{"rejects unrelated registry", "index.docker.io", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, p, err := NewACRCredentialsHelper().Get(tc.serverURL)
			if u != "" || p != "" {
				t.Fatalf("Get(%q) returned credentials %q/%q, want none", tc.serverURL, u, p)
			}
			if gotNotFound := credentials.IsErrCredentialsNotFound(err); gotNotFound != tc.wantNotFound {
				t.Fatalf("Get(%q): err = %v; credentials not found = %t, want %t", tc.serverURL, err, gotNotFound, tc.wantNotFound)
			}
		})
	}
}

// TestGetCommandRejectsNonACRHostsAsNotFound drives the helper the way the
// standalone docker-credential-acr-env binary does: cmd/get.go passes stdin
// to credentials.Serve, which trims the server URL and calls Get.
// (Chainguard, GO-2026-6225.)
func TestGetCommandRejectsNonACRHostsAsNotFound(t *testing.T) {
	isolateAzure(t)
	for _, tc := range []struct {
		name         string
		stdin        string
		wantNotFound bool
	}{
		{"passes ACR registry to token request", "foo.azurecr.io\n", false},
		{"rejects azurecr.io lookalike", "evil.azurecr.io.attacker.com\n", true},
		{"rejects azurecr.io lookalike with surrounding spaces", "  evil.azurecr.io.attacker.com  \n", true},
		{"rejects MCR lookalike", "mcr.microsoft.com.attacker.com\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := credentials.HandleCommand(NewACRCredentialsHelper(), credentials.ActionGet, strings.NewReader(tc.stdin), &out)
			if gotNotFound := credentials.IsErrCredentialsNotFound(err); gotNotFound != tc.wantNotFound {
				t.Fatalf("get with stdin %q: err = %v, stdout = %q; credentials not found = %t, want %t", tc.stdin, err, out.String(), gotNotFound, tc.wantNotFound)
			}
			if out.Len() != 0 {
				t.Fatalf("get with stdin %q wrote credentials %q, want none", tc.stdin, out.String())
			}
		})
	}
}
