/*
Copyright © 2020 Chris Mellard chris.mellard@icloud.com

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
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/Azure/go-autorest/autorest/azure/auth"
	"github.com/chainguard-dev/kaniko/third_party/docker-credential-acr-env/pkg/registry"
	"github.com/chainguard-dev/kaniko/third_party/docker-credential-acr-env/pkg/token"
	"github.com/docker/docker-credential-helpers/credentials"
)

// Chainguard patch (GO-2026-6225): upstream's acrRE was unanchored, so any
// hostname merely containing ".azurecr.io" (for example
// "evil.azurecr.io.attacker.com") was treated as ACR and sent an Azure AD
// derived token. acrHostRE is anchored at both ends and only admits
// well-formed lower-case DNS labels in front of an ACR domain.
var acrHostRE = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+azurecr\.(?:io|cn|de|us)$`)

// portRE matches the digits of an explicit port; the range is checked separately.
var portRE = regexp.MustCompile(`^[0-9]{1,5}$`)

const (
	mcrHostname   = "mcr.microsoft.com"
	tokenUsername = "<token>"
)

type ACRCredHelper struct {
}

func NewACRCredentialsHelper() credentials.Helper {
	return &ACRCredHelper{}
}

func (a ACRCredHelper) Add(_ *credentials.Credentials) error {
	return errors.New("list is unimplemented")
}

func (a ACRCredHelper) Delete(_ string) error {
	return errors.New("list is unimplemented")
}

// isACRRegistry reports whether input is a bare ACR or MCR registry host,
// optionally with a port.
//
// Chainguard patch (GO-2026-6225). Rules:
//   - input must be exactly host[:port]. url.Parse splits userinfo, path,
//     query and fragment off before Host and decodes percent-escapes in
//     Hostname(), so the Host == input comparison is load-bearing: it is the
//     only thing that rejects inputs such as "[::1%25.azurecr.io]" or
//     "foo%C3%A9.azurecr.io", whose decoded Hostname() would otherwise look
//     like an ACR host.
//   - an explicit port must be 1-65535 (an empty port, as in "host:", is
//     rejected).
//   - the hostname must be mcr.microsoft.com, or match acrHostRE: anchored,
//     no trailing dot, no empty or malformed labels.
//   - matching is case-sensitive, as it was upstream. Registry hosts are
//     normally lower case; widening to mixed case would change what is
//     forwarded to the token exchange, so it is deliberately not done here.
func isACRRegistry(input string) bool {
	serverURL, err := url.Parse("https://" + input)
	if err != nil || serverURL.Host != input {
		return false
	}
	if i := strings.LastIndexByte(input, ':'); i >= 0 {
		port := input[i+1:]
		if !portRE.MatchString(port) {
			return false
		}
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return false
		}
	}
	host := serverURL.Hostname()
	return host == mcrHostname || acrHostRE.MatchString(host)
}

func (a ACRCredHelper) Get(serverURL string) (string, string, error) {
	if !isACRRegistry(serverURL) {
		// Chainguard patch: report "credentials not found" rather than a
		// generic error, so that docker/cli and go-containerregistry treat a
		// non-ACR host as anonymous when this binary is used as a global
		// credsStore, instead of failing the whole lookup.
		return "", "", credentials.NewErrCredentialsNotFound()
	}

	spToken, settings, err := token.GetServicePrincipalTokenFromEnvironment()
	if err != nil {
		return "", "", fmt.Errorf("failed to acquire sp token %w", err)
	}
	refreshToken, err := registry.GetRegistryRefreshTokenFromAADExchange(serverURL, spToken, settings.Values[auth.TenantID])
	if err != nil {
		return "", "", fmt.Errorf("failed to acquire refresh token %w", err)
	}
	return tokenUsername, refreshToken, nil
}

func (a ACRCredHelper) List() (map[string]string, error) {
	return nil, errors.New("list is unimplemented")
}
