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

		// Chainguard: bracketed names. These document net/url behaviour, not
		// a check in isACRRegistry, which has no bracket handling of its own:
		// url.Parse only accepts an IPv6 address inside brackets, so these
		// fail to parse. On a Go release older than 1.24.8 or 1.25.2 they
		// would parse, Hostname() would strip the brackets, and the result
		// would match acrHostRE; go.mod's Go version rules those out.
		{"[foo.azurecr.io]", false},
		{"[foo.azurecr.io]:443", false},
		{"[mcr.microsoft.com]", false},

		// Chainguard: IPv6 literals. Each is rejected by a different check.
		{"[::1]", false},                      // port check: the last ':' is inside the literal, so the port is "1]"
		{"[::1]:443", false},                  // acrHostRE
		{"[fe80::1%25foo.azurecr.io]", false}, // Host == input: url.Parse unescapes the %25 zone marker

		// Chainguard: one input per class that url.Parse, or the
		// Host == input comparison, rejects before isACRRegistry's own
		// checks run. They document that these inputs fail, but no single
		// change to isACRRegistry flips them.
		{"foo.azurecr.io\n", false},                // control character
		{" foo.azurecr.io", false},                 // leading space
		{"foo.azurecr.io\x00.attacker.com", false}, // NUL
		{"foo.azurecr.io\\@attacker.com", false},   // backslash
		{"https://foo.azurecr.io", false},          // scheme prefix
		{"foo.azurecr.io%2eattacker.com", false},   // percent-encoded dot

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
// while it reads its settings, before it chooses an auth method or contacts
// Azure AD or the instance metadata service. go-autorest rejects the unknown
// AZURE_ENVIRONMENT, so this one variable is the whole guard; the
// "passes ACR registry" rows below check that it still fails at that stage.
// (Chainguard, GO-2026-6225.)
func isolateAzure(t *testing.T) {
	t.Helper()
	t.Setenv("AZURE_ENVIRONMENT", "kaniko-unit-test-no-such-cloud")
}

// settingsErr is part of the error Get returns when isolateAzure's unknown
// AZURE_ENVIRONMENT stops it reading its Azure settings (pkg/token/token.go).
const settingsErr = "failed to get auth settings from environment"

// getCases drives both Get tests below. A real ACR host must get past the
// host check and fail at the settings stage, so the "not found" rows can't
// pass just because every host fails. (Chainguard, GO-2026-6225.)
var getCases = []struct {
	name         string
	serverURL    string
	wantNotFound bool
}{
	{"passes ACR registry to token request", "foo.azurecr.io", false},
	{"rejects azurecr.io lookalike", "evil.azurecr.io.attacker.com", true},
	{"rejects MCR lookalike", "mcr.microsoft.com.attacker.com", true},
	{"rejects trailing dot", "foo.azurecr.io.", true},
	{"rejects userinfo", "foo.azurecr.io@attacker.com", true},
	{"rejects unrelated registry", "index.docker.io", true},
}

// checkGetErr fails t unless err is what a "credentials not found" row or a
// "passes ACR registry" row should produce. Not-found rows must carry the
// exact message docker-credential-helpers' client matches when it reads the
// helper's stdout (IsErrCredentialsNotFoundMessage), not just the Go type:
// a wrapped not-found error keeps the type but changes the text, and then
// every non-ACR lookup through the binary as a global credsStore fails.
func checkGetErr(t *testing.T, input string, err error, wantNotFound bool) {
	t.Helper()
	if wantNotFound {
		if err == nil || !credentials.IsErrCredentialsNotFound(err) || !credentials.IsErrCredentialsNotFoundMessage(err.Error()) {
			t.Fatalf("%q: err = %v, want the exact credentials-not-found error", input, err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), settingsErr) {
		t.Fatalf("%q: err = %v, want an error containing %q", input, err, settingsErr)
	}
}

// TestGetRejectsNonACRHostsAsNotFound checks that Get fails fast with a
// "credentials not found" error for non-ACR hosts, before any Azure token
// is requested. (Chainguard, GO-2026-6225.)
func TestGetRejectsNonACRHostsAsNotFound(t *testing.T) {
	isolateAzure(t)
	for _, tc := range getCases {
		t.Run(tc.name, func(t *testing.T) {
			u, p, err := NewACRCredentialsHelper().Get(tc.serverURL)
			if u != "" || p != "" {
				t.Fatalf("Get(%q) returned credentials %q/%q, want none", tc.serverURL, u, p)
			}
			checkGetErr(t, tc.serverURL, err, tc.wantNotFound)
		})
	}
}

// TestGetCommandRejectsNonACRHostsAsNotFound drives the helper the way the
// standalone docker-credential-acr-env binary does: cmd/get.go passes stdin
// to credentials.Serve, which trims the server URL and calls Get. It runs
// getCases as stdin lines, plus a padded row only the command path can see.
// (Chainguard, GO-2026-6225.)
func TestGetCommandRejectsNonACRHostsAsNotFound(t *testing.T) {
	isolateAzure(t)
	type row struct {
		name         string
		stdin        string
		wantNotFound bool
	}
	rows := []row{{"rejects azurecr.io lookalike with surrounding spaces", "  evil.azurecr.io.attacker.com  \n", true}}
	for _, tc := range getCases {
		rows = append(rows, row{tc.name, tc.serverURL + "\n", tc.wantNotFound})
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := credentials.HandleCommand(NewACRCredentialsHelper(), credentials.ActionGet, strings.NewReader(tc.stdin), &out)
			if out.Len() != 0 {
				t.Fatalf("get with stdin %q wrote credentials %q, want none", tc.stdin, out.String())
			}
			checkGetErr(t, tc.stdin, err, tc.wantNotFound)
		})
	}
}
