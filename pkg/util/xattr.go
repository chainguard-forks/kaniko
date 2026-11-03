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

package util

import "errors"

// lgetxattr and lsetxattr (xattr_linux.go, xattr_unsupported.go) are ported
// from github.com/docker/docker/pkg/system (Apache-2.0, see
// third_party/moby/LICENSE) so that kaniko no longer imports the docker/docker
// module. Semantics are unchanged: lgetxattr returns a nil slice and a nil
// error when the attribute is not set and grows its buffer on ERANGE, and
// failures are wrapped in *xattrError so callers can errors.Is the errno.

// errNotSupportedPlatform means the platform is not supported.
var errNotSupportedPlatform = errors.New("platform and architecture is not supported")

type xattrError struct {
	op   string
	attr string
	path string
	err  error
}

func (e *xattrError) Error() string { return e.op + " " + e.attr + " " + e.path + ": " + e.err.Error() }

func (e *xattrError) Unwrap() error { return e.err }
