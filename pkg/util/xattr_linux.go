//go:build linux
// +build linux

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

import (
	"errors"

	"golang.org/x/sys/unix"
)

// lgetxattr retrieves the value of the extended attribute identified by attr
// and associated with the given path in the file system.
// It returns a nil slice and nil error if the xattr is not set.
func lgetxattr(path string, attr string) ([]byte, error) {
	sysErr := func(err error) ([]byte, error) {
		return nil, &xattrError{op: "lgetxattr", attr: attr, path: path, err: err}
	}

	// Start with a 128 length byte array
	dest := make([]byte, 128)
	sz, errno := unix.Lgetxattr(path, attr, dest)

	for errors.Is(errno, unix.ERANGE) {
		// Buffer too small, use zero-sized buffer to get the actual size
		sz, errno = unix.Lgetxattr(path, attr, []byte{})
		if errno != nil {
			return sysErr(errno)
		}
		dest = make([]byte, sz)
		sz, errno = unix.Lgetxattr(path, attr, dest)
	}

	switch {
	case errors.Is(errno, unix.ENODATA):
		return nil, nil
	case errno != nil:
		return sysErr(errno)
	}

	return dest[:sz], nil
}

// lsetxattr sets the value of the extended attribute identified by attr
// and associated with the given path in the file system.
func lsetxattr(path string, attr string, data []byte, flags int) error {
	if err := unix.Lsetxattr(path, attr, data, flags); err != nil {
		return &xattrError{op: "lsetxattr", attr: attr, path: path, err: err}
	}
	return nil
}
