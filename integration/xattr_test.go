/*
Copyright 2018 Google LLC

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

package integration

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

const (
	// capSetuid is a vfs_cap_data blob granting CAP_SETUID (revision 2).
	capSetuid = "\x01\x00\x00\x02\x00\x80\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"
	// posixACL is a POSIX ACL granting uid 65534 read access.
	posixACL = "\x02\x00\x00\x00" +
		"\x01\x00\x06\x00\xff\xff\xff\xff" + // user::rw-
		"\x02\x00\x04\x00\xfe\xff\x00\x00" + // user:65534:r--
		"\x04\x00\x00\x00\xff\xff\xff\xff" + // group::---
		"\x10\x00\x04\x00\xff\xff\xff\xff" + // mask::r--
		"\x20\x00\x00\x00\xff\xff\xff\xff" // other::---
)

// pushBaseWithXattrs pushes alpine plus a layer in which /usr/bin/cap carries a
// file capability and /etc/secret carries a POSIX ACL, both as SCHILY.xattr
// records, and returns the reference.
func pushBaseWithXattrs(t *testing.T, ref string) {
	t.Helper()

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	add := func(name, body string, mode int64, key, val string) {
		hdr := &tar.Header{
			Typeflag:   tar.TypeReg,
			Name:       name,
			Mode:       mode,
			Size:       int64(len(body)),
			PAXRecords: map[string]string{key: val},
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	add("usr/bin/cap", "#!/bin/sh\n", 0o755, "SCHILY.xattr.security.capability", capSetuid)
	add("etc/secret", "secret\n", 0o600, "SCHILY.xattr.system.posix_acl_access", posixACL)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	layer, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(b)), nil
	})
	if err != nil {
		t.Fatal(err)
	}

	alpineRef, err := name.ParseReference("alpine:3.20")
	if err != nil {
		t.Fatal(err)
	}
	alpine, err := remote.Image(alpineRef)
	if err != nil {
		t.Fatalf("pulling alpine: %v", err)
	}
	img, err := mutate.AppendLayers(alpine, layer)
	if err != nil {
		t.Fatal(err)
	}
	dst, err := name.ParseReference(ref, name.WeakValidation)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(dst, img); err != nil {
		t.Fatalf("pushing base image %s: %v", ref, err)
	}
}

// xattrRecords lists the SCHILY.xattr.* and SCHILY.acl.* records in every layer
// of a pushed image.
func xattrRecords(t *testing.T, ref string) []string {
	t.Helper()
	r, err := name.ParseReference(ref, name.WeakValidation)
	if err != nil {
		t.Fatal(err)
	}
	img, err := remote.Image(r)
	if err != nil {
		t.Fatalf("pulling %s: %v", ref, err)
	}
	layers, err := img.Layers()
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for i, l := range layers {
		rc, err := l.Uncompressed()
		if err != nil {
			t.Fatal(err)
		}
		tr := tar.NewReader(rc)
		for {
			hdr, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			for k := range hdr.PAXRecords {
				if strings.HasPrefix(k, "SCHILY.xattr.") || strings.HasPrefix(k, "SCHILY.acl.") {
					found = append(found, fmt.Sprintf("layer %d %s %s", i, hdr.Name, k))
				}
			}
		}
		rc.Close()
	}
	return found
}

// TestStripXattrsFromBaseLayers builds FROM a base whose layer carries a file
// capability and a POSIX ACL, and checks what the pushed image contains. The
// base layer is pushed as it is unless kaniko rewrites it, so this only shows
// up in the registry, not in the build filesystem. By default kaniko keeps the
// records, like Docker, BuildKit and containerd; --strip-xattrs removes them.
func TestStripXattrsFromBaseLayers(t *testing.T) {
	base := GetKanikoImage(config.imageRepo, "xattr-base")
	pushBaseWithXattrs(t, base)

	cases := []struct {
		name       string
		runLine    string
		flags      []string
		wantRecord bool
	}{
		{name: "default-keeps-xattrs", runLine: "RUN echo built", wantRecord: true},
		{name: "unchanged-build", runLine: "RUN echo built", flags: []string{"--strip-xattrs"}},
		{name: "single-snapshot", runLine: "RUN echo built", flags: []string{"--strip-xattrs", "--single-snapshot"}},
		{name: "file-modified", runLine: "RUN touch /usr/bin/cap", flags: []string{"--strip-xattrs"}},
		{name: "compression-level", runLine: "RUN echo built", flags: []string{"--strip-xattrs", "--compression-level=9"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctxDir := t.TempDir()
			df := fmt.Sprintf("FROM %s\n%s\n", base, tc.runLine)
			if err := os.WriteFile(filepath.Join(ctxDir, "Dockerfile"), []byte(df), 0o644); err != nil {
				t.Fatal(err)
			}
			out := GetKanikoImage(config.imageRepo, "xattr-out") + ":" + tc.name

			args := []string{"run", "--net=host", "-v", ctxDir + ":/workspace"}
			args = addServiceAccountFlags(args, config.serviceAccount)
			args = append(args, ExecutorImage,
				"-f", "/workspace/Dockerfile", "-c", "dir:///workspace", "-d", out)
			args = append(args, tc.flags...)
			if o, err := RunCommandWithoutTest(exec.Command("docker", args...)); err != nil {
				t.Fatalf("kaniko build failed: %v\n%s", err, o)
			}

			got := xattrRecords(t, out)
			if tc.wantRecord && len(got) != 2 {
				t.Errorf("expected the capability and the ACL to be kept, got %v", got)
			}
			if !tc.wantRecord && len(got) != 0 {
				t.Errorf("expected no xattr or ACL records in the pushed image, got %v", got)
			}
		})
	}
}

// TestStripXattrsMultiStage covers a later stage built on an earlier one, whose
// base is read from /kaniko/stages and is not scanned again, and COPY --from of
// a remote image, which is unpacked before any stage is built. With
// --strip-xattrs neither may bring a capability or ACL record into the result.
func TestStripXattrsMultiStage(t *testing.T) {
	base := GetKanikoImage(config.imageRepo, "xattr-base-ms")
	pushBaseWithXattrs(t, base)

	ctxDir := t.TempDir()
	df := fmt.Sprintf("FROM %s AS first\nRUN echo first\n\nFROM first\nRUN echo second\n\nFROM alpine:3.20\nCOPY --from=%s /usr/bin/cap /cap\nCOPY --from=first /etc/secret /secret\n", base, base)
	if err := os.WriteFile(filepath.Join(ctxDir, "Dockerfile"), []byte(df), 0o644); err != nil {
		t.Fatal(err)
	}
	out := GetKanikoImage(config.imageRepo, "xattr-out-ms")

	args := []string{"run", "--net=host", "-v", ctxDir + ":/workspace"}
	args = addServiceAccountFlags(args, config.serviceAccount)
	args = append(args, ExecutorImage,
		"-f", "/workspace/Dockerfile", "-c", "dir:///workspace", "-d", out, "--strip-xattrs")
	if o, err := RunCommandWithoutTest(exec.Command("docker", args...)); err != nil {
		t.Fatalf("kaniko build failed: %v\n%s", err, o)
	}
	if got := xattrRecords(t, out); len(got) != 0 {
		t.Errorf("expected no xattr or ACL records in the pushed image, got %v", got)
	}
}
