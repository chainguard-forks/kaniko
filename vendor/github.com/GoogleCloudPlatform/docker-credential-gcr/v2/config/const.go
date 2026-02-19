// Copyright 2016 Google, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package config provides variables used in configuring the behavior of the app.
package config

import (
	"context"
	"fmt"
	"regexp"
	"runtime/debug"
	"strings"

	"golang.org/x/oauth2/google"
)

const (
	// GCRCredHelperClientID is the client_id to be used when performing the
	// OAuth2 Authorization Code grant flow.
	// See https://developers.google.com/identity/protocols/OAuth2InstalledApp
	GCRCredHelperClientID = "99426463878-o7n0bshgue20tdpm25q4at0vs2mr4utq.apps.googleusercontent.com"

	// GCRCredHelperClientNotSoSecret is the client_secret to be used when
	// performing the OAuth2 Authorization Code grant flow.
	// See https://developers.google.com/identity/protocols/OAuth2InstalledApp
	GCRCredHelperClientNotSoSecret = "HpVi8cnKx8AAkddzaNrSWmS8"
)

// Version can be set via:
// -ldflags="-X 'github.com/GoogleCloudPlatform/docker-credential-gcr/v2/config.Version=$TAG'"
var Version string

func init() {
	if Version == "" {
		if i, ok := debug.ReadBuildInfo(); ok {
			Version = i.Main.Version
		}
	}
	Version = strings.TrimPrefix(Version, "v")
	re := regexp.MustCompile(`^([0-9]+(?:[\._][0-9]+)*)(?:-[0-9]+-[0-9a-f]+)?$`)
	if matches := re.FindStringSubmatch(Version); matches != nil {
		GcrOAuth2Username = fmt.Sprintf("_dcgcr_%s_token", strings.ReplaceAll(matches[1], ".", "_"))
	} else {
		GcrOAuth2Username = "_dcgcr_0_0_0_token"
	}
}

// DefaultGCRRegistries contains the list of default registries to authenticate for.
var DefaultGCRRegistries = [...]string{
	"gcr.io",
	"us.gcr.io",
	"eu.gcr.io",
	"asia.gcr.io",
	"marketplace.gcr.io",
}

// DefaultARRegistries contains the list of default registries for Artifact
// Registry.  If the --include-artifact-registry flag is supplied then these
// are added in addition to the GCR Registries.
var DefaultARRegistries = [...]string{
	"africa-south1-docker.pkg.dev",
	"docker.africa-south1.rep.pkg.dev",
	"asia-docker.pkg.dev",
	"asia-east1-docker.pkg.dev",
	"docker.asia-east1.rep.pkg.dev",
	"asia-east2-docker.pkg.dev",
	"docker.asia-east2.rep.pkg.dev",
	"asia-northeast1-docker.pkg.dev",
	"docker.asia-northeast1.rep.pkg.dev",
	"asia-northeast2-docker.pkg.dev",
	"docker.asia-northeast2.rep.pkg.dev",
	"asia-northeast3-docker.pkg.dev",
	"docker.asia-northeast3.rep.pkg.dev",
	"asia-south1-docker.pkg.dev",
	"docker.asia-south1.rep.pkg.dev",
	"asia-south2-docker.pkg.dev",
	"docker.asia-south2.rep.pkg.dev",
	"asia-southeast1-docker.pkg.dev",
	"docker.asia-southeast1.rep.pkg.dev",
	"asia-southeast2-docker.pkg.dev",
	"docker.asia-southeast2.rep.pkg.dev",
	"australia-southeast1-docker.pkg.dev",
	"docker.australia-southeast1.rep.pkg.dev",
	"australia-southeast2-docker.pkg.dev",
	"docker.australia-southeast2.rep.pkg.dev",
	"europe-docker.pkg.dev",
	"europe-central2-docker.pkg.dev",
	"docker.europe-central2.rep.pkg.dev",
	"europe-north1-docker.pkg.dev",
	"docker.europe-north1.rep.pkg.dev",
	"europe-north2-docker.pkg.dev",
	"europe-southwest1-docker.pkg.dev",
	"docker.europe-southwest1.rep.pkg.dev",
	"europe-west1-docker.pkg.dev",
	"docker.europe-west1.rep.pkg.dev",
	"europe-west10-docker.pkg.dev",
	"docker.europe-west10.rep.pkg.dev",
	"europe-west12-docker.pkg.dev",
	"docker.europe-west12.rep.pkg.dev",
	"europe-west2-docker.pkg.dev",
	"docker.europe-west2.rep.pkg.dev",
	"europe-west3-docker.pkg.dev",
	"docker.europe-west3.rep.pkg.dev",
	"europe-west4-docker.pkg.dev",
	"docker.europe-west4.rep.pkg.dev",
	"europe-west6-docker.pkg.dev",
	"docker.europe-west6.rep.pkg.dev",
	"europe-west8-docker.pkg.dev",
	"docker.europe-west8.rep.pkg.dev",
	"europe-west9-docker.pkg.dev",
	"docker.europe-west9.rep.pkg.dev",
	"me-central1-docker.pkg.dev",
	"docker.me-central1.rep.pkg.dev",
	"me-central2-docker.pkg.dev",
	"docker.me-central2.rep.pkg.dev",
	"me-west1-docker.pkg.dev",
	"docker.me-west1.rep.pkg.dev",
	"northamerica-northeast1-docker.pkg.dev",
	"docker.northamerica-northeast1.rep.pkg.dev",
	"northamerica-northeast2-docker.pkg.dev",
	"docker.northamerica-northeast2.rep.pkg.dev",
	"northamerica-south1-docker.pkg.dev",
	"southamerica-east1-docker.pkg.dev",
	"docker.southamerica-east1.rep.pkg.dev",
	"southamerica-west1-docker.pkg.dev",
	"docker.southamerica-west1.rep.pkg.dev",
	"us-docker.pkg.dev",
	"us-central1-docker.pkg.dev",
	"docker.us-central1.rep.pkg.dev",
	"us-east1-docker.pkg.dev",
	"docker.us-east1.rep.pkg.dev",
	"us-east4-docker.pkg.dev",
	"docker.us-east4.rep.pkg.dev",
	"us-east5-docker.pkg.dev",
	"docker.us-east5.rep.pkg.dev",
	"us-south1-docker.pkg.dev",
	"docker.us-south1.rep.pkg.dev",
	"us-west1-docker.pkg.dev",
	"docker.us-west1.rep.pkg.dev",
	"us-west2-docker.pkg.dev",
	"docker.us-west2.rep.pkg.dev",
	"us-west3-docker.pkg.dev",
	"docker.us-west3.rep.pkg.dev",
	"us-west4-docker.pkg.dev",
	"docker.us-west4.rep.pkg.dev",
	"us-west8-docker.pkg.dev",
	"docker.us-west8.rep.pkg.dev",
	"europe-north2-docker.pkg.dev",
	"asia-southeast3-docker.pkg.dev",
}

// SupportedGCRTokenSources maps config keys to plain english explanations for
// where the helper should search for a GCR access token.
var SupportedGCRTokenSources = map[string]string{
	"env":    "Application default credentials or GCE/AppEngine metadata.",
	"gcloud": "'gcloud auth print-access-token'",
	"store":  "The file store maintained by the credential helper.",
}

// GCROAuth2Endpoint describes the oauth2.Endpoint to be used when
// authenticating a GCR user.
var GCROAuth2Endpoint = google.Endpoint

// GCRScopes is/are the OAuth2 scope(s) to request during access_token creation.
var GCRScopes = []string{"https://www.googleapis.com/auth/devstorage.read_write"}

// OAuthHTTPContext is the HTTP context to use when performing OAuth2 calls.
var OAuthHTTPContext = context.Background()

// GcrOAuth2Username is the Basic auth username accompanying Docker requests to GCR.
var GcrOAuth2Username string
