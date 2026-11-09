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

package util

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/chainguard-dev/kaniko/pkg/config"
	"github.com/docker/docker/pkg/system"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/sirupsen/logrus"
)

// capBlob is a vfs_cap_data blob granting CAP_SETUID (revision 2, permitted).
const capBlob = "\x01\x00\x00\x02\x00\x80\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"

type tarEntry struct {
	name string
	body string
	pax  map[string]string
	// xattrs uses the deprecated Header.Xattrs field.
	xattrs map[string]string
}

func buildLayer(t *testing.T, entries []tarEntry, opts ...tarball.LayerOption) v1.Layer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		hdr := &tar.Header{
			Typeflag:   tar.TypeReg,
			Name:       e.name,
			Mode:       0o640,
			Uid:        1,
			Gid:        2,
			Size:       int64(len(e.body)),
			PAXRecords: e.pax,
			Xattrs:     e.xattrs, //nolint:staticcheck
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	l, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(b)), nil
	}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func buildImage(t *testing.T, layers ...v1.Layer) v1.Image {
	t.Helper()
	img := empty.Image
	for i, l := range layers {
		var err error
		img, err = mutate.Append(img, mutate.Addendum{
			Layer:   l,
			History: v1.History{CreatedBy: "step " + string(rune('A'+i))},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return img
}

// readLayer returns the headers and bodies of every entry in a layer.
func readLayer(t *testing.T, l v1.Layer) ([]*tar.Header, map[string]string) {
	t.Helper()
	r, err := l.Uncompressed()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var hdrs []*tar.Header
	bodies := map[string]string{}
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		hdrs = append(hdrs, h)
		bodies[h.Name] = string(b)
	}
	return hdrs, bodies
}

func useKanikoDir(t *testing.T) {
	t.Helper()
	old := config.KanikoDir
	config.KanikoDir = t.TempDir()
	t.Cleanup(func() { config.KanikoDir = old })
}

// stripFiles lists the rewritten layers kept under the kaniko directory.
func stripFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(xattrStripDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestStripImageXattrs(t *testing.T) {
	useKanikoDir(t)

	capLayer := buildLayer(t, []tarEntry{
		{name: "usr/bin/cap", body: "binary", pax: map[string]string{"SCHILY.xattr.security.capability": capBlob}},
		{name: "etc/secret", body: "s3cret", pax: map[string]string{"SCHILY.xattr.system.posix_acl_access": "acl"}},
		{name: "etc/plain", body: "plain"},
	})
	cleanLayer := buildLayer(t, []tarEntry{{name: "etc/clean", body: "clean"}})
	legacyLayer := buildLayer(t, []tarEntry{
		// The deprecated Xattrs field is written out as SCHILY.xattr.* records.
		{name: "usr/bin/legacy", body: "x", xattrs: map[string]string{"security.capability": capBlob}},
		{name: "etc/other", body: "o", pax: map[string]string{"SCHILY.acl.access": "user::rw-", "user.keep-me-not": "v"}},
	})
	img := buildImage(t, capLayer, cleanLayer, legacyLayer)

	got, err := StripImageXattrs(img, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Only the two rewritten layers are kept on disk; the layer without xattrs
	// left no temporary file behind.
	if n := len(stripFiles(t)); n != 2 {
		t.Errorf("got %d rewritten layer files, want 2", n)
	}

	layers, err := got.Layers()
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 3 {
		t.Fatalf("got %d layers, want 3", len(layers))
	}

	// The layer without xattrs keeps its digest; the other two are rewritten.
	for i, wantSame := range []bool{false, true, false} {
		orig, _ := img.Layers()
		od, _ := orig[i].Digest()
		nd, _ := layers[i].Digest()
		if (od == nd) != wantSame {
			t.Errorf("layer %d: digest same = %v, want %v (%s -> %s)", i, od == nd, wantSame, od, nd)
		}
		hdrs, _ := readLayer(t, layers[i])
		for _, h := range hdrs {
			if names := xattrRecordNames(h); len(names) != 0 {
				t.Errorf("layer %d entry %s still has %v", i, h.Name, names)
			}
		}
	}

	// Everything except the xattr records is preserved.
	hdrs, bodies := readLayer(t, layers[0])
	if len(hdrs) != 3 || bodies["usr/bin/cap"] != "binary" || bodies["etc/secret"] != "s3cret" || bodies["etc/plain"] != "plain" {
		t.Errorf("layer 0 content changed: %v", bodies)
	}
	for _, h := range hdrs {
		if h.Mode != 0o640 || h.Uid != 1 || h.Gid != 2 {
			t.Errorf("%s: mode/uid/gid changed: %o %d %d", h.Name, h.Mode, h.Uid, h.Gid)
		}
	}
	// Unrelated PAX records survive.
	hdrs, _ = readLayer(t, layers[2])
	if hdrs[1].PAXRecords["user.keep-me-not"] != "v" {
		t.Errorf("unrelated PAX record was removed: %v", hdrs[1].PAXRecords)
	}

	// The config is consistent with the new layers and keeps the history.
	cf, err := got.ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	ocf, _ := img.ConfigFile()
	if len(cf.History) != len(ocf.History) {
		t.Fatalf("history length %d, want %d", len(cf.History), len(ocf.History))
	}
	for i := range cf.History {
		if cf.History[i].CreatedBy != ocf.History[i].CreatedBy {
			t.Errorf("history %d changed: %q", i, cf.History[i].CreatedBy)
		}
	}
	if len(cf.RootFS.DiffIDs) != 3 {
		t.Fatalf("got %d diff IDs", len(cf.RootFS.DiffIDs))
	}
	for i, l := range layers {
		d, _ := l.DiffID()
		if cf.RootFS.DiffIDs[i] != d {
			t.Errorf("diff ID %d does not match its layer", i)
		}
	}
	if m, _ := got.MediaType(); m == "" {
		t.Error("empty media type")
	}
	if err := validateImage(got); err != nil {
		t.Error(err)
	}
}

func validateImage(img v1.Image) error {
	layers, err := img.Layers()
	if err != nil {
		return err
	}
	for _, l := range layers {
		r, err := l.Compressed()
		if err != nil {
			return err
		}
		_, err = io.Copy(io.Discard, r)
		r.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func TestStripImageXattrsNoRecordsReturnsSameImage(t *testing.T) {
	useKanikoDir(t)
	img := buildImage(t, buildLayer(t, []tarEntry{{name: "a", body: "a"}}))
	got, err := StripImageXattrs(img, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got != img {
		t.Error("image without xattrs was rebuilt")
	}
	if n := stripFiles(t); len(n) != 0 {
		t.Errorf("temporary files left behind: %v", n)
	}
	od, _ := img.Digest()
	nd, _ := got.Digest()
	if od != nd {
		t.Error("digest changed")
	}
}

func TestExtractFileXattrs(t *testing.T) {
	for _, strip := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "strip"}[strip], func(t *testing.T) {
			if !strip && os.Geteuid() != 0 {
				t.Skip("setting security.capability needs root")
			}
			SetStripXattrs(strip)
			t.Cleanup(func() { SetStripXattrs(false) })

			dir := t.TempDir()
			path := filepath.Join(dir, "cap")
			if err := os.WriteFile(path, nil, 0o755); err != nil {
				t.Fatal(err)
			}
			hdr := &tar.Header{
				Typeflag:   tar.TypeReg,
				Name:       "cap",
				PAXRecords: map[string]string{"SCHILY.xattr.security.capability": capBlob},
			}
			hdr.Xattrs = map[string]string{"security.capability": capBlob} //nolint:staticcheck
			if err := writeSecurityXattrToTarFile(path, hdr); err != nil {
				t.Fatal(err)
			}
			got, err := system.Lgetxattr(path, securityCapabilityXattr)
			if strip {
				if got != nil {
					t.Fatalf("capability was written with --strip-xattrs: %q", got)
				}
				return
			}
			if err != nil || got == nil {
				t.Fatalf("capability was not kept by default: %v", err)
			}
		})
	}
}

func TestStripImageXattrsUncompressedMediaType(t *testing.T) {
	useKanikoDir(t)
	for _, tc := range []struct{ in, want types.MediaType }{
		{types.OCIUncompressedLayer, types.OCILayer},
		{types.DockerUncompressedLayer, types.DockerLayer},
		{types.OCILayer, types.OCILayer},
		{types.DockerLayer, types.DockerLayer},
	} {
		t.Run(string(tc.in), func(t *testing.T) {
			l := buildLayer(t, []tarEntry{
				{name: "usr/bin/cap", body: "b", pax: map[string]string{"SCHILY.xattr.security.capability": capBlob}},
			}, tarball.WithMediaType(tc.in))
			nl, n, err := stripLayerXattrs(l, 0)
			if err != nil {
				t.Fatal(err)
			}
			if nl == nil || n != 1 {
				t.Fatalf("layer was not rewritten: %v %d", nl, n)
			}
			got, err := nl.MediaType()
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("media type %s, want %s", got, tc.want)
			}
			// The blob must be compressed, since that is what the type says.
			rc, err := nl.Compressed()
			if err != nil {
				t.Fatal(err)
			}
			defer rc.Close()
			magic := make([]byte, 2)
			if _, err := io.ReadFull(rc, magic); err != nil {
				t.Fatal(err)
			}
			if magic[0] != 0x1f || magic[1] != 0x8b {
				t.Errorf("blob is not gzip: % x", magic)
			}
		})
	}
}

func TestStripImageXattrsCompressionLevel(t *testing.T) {
	useKanikoDir(t)
	var b bytes.Buffer
	for i := 0; i < 4000; i++ {
		fmt.Fprintf(&b, "line %c kaniko strips xattrs from base layers number %c\n", 'a'+i%26, 'A'+(i*7)%26)
	}
	size := func(level int) int64 {
		l := buildLayer(t, []tarEntry{
			{name: "usr/bin/cap", body: b.String(), pax: map[string]string{"SCHILY.xattr.security.capability": capBlob}},
		})
		nl, _, err := stripLayerXattrs(l, level)
		if err != nil || nl == nil {
			t.Fatalf("layer was not rewritten: %v", err)
		}
		n, err := nl.Size()
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	// go-containerregistry compresses at its default (fastest) level unless it is
	// told otherwise, so a smaller blob at level 9 shows the level is passed on.
	def, best := size(0), size(9)
	if best >= def {
		t.Errorf("level 9 produced %d bytes, not fewer than the default level (%d)", best, def)
	}
}

func TestRemoveXattrStripFiles(t *testing.T) {
	useKanikoDir(t)
	l := buildLayer(t, []tarEntry{
		{name: "usr/bin/cap", body: "b", pax: map[string]string{"SCHILY.xattr.security.capability": capBlob}},
	})
	if _, _, err := stripLayerXattrs(l, 0); err != nil {
		t.Fatal(err)
	}
	if len(stripFiles(t)) != 1 {
		t.Fatalf("expected one rewritten layer file, got %v", stripFiles(t))
	}
	if err := RemoveXattrStripFiles(); err != nil {
		t.Fatal(err)
	}
	if n := stripFiles(t); len(n) != 0 {
		t.Errorf("files left after RemoveXattrStripFiles: %v", n)
	}
	// Removing when there is nothing to remove is not an error.
	if err := RemoveXattrStripFiles(); err != nil {
		t.Error(err)
	}
}

type captureHook struct{ entries []*logrus.Entry }

func (h *captureHook) Levels() []logrus.Level { return logrus.AllLevels }
func (h *captureHook) Fire(e *logrus.Entry) error {
	h.entries = append(h.entries, e)
	return nil
}

// Only security.capability is ever applied to the filesystem, so only a record
// for it is worth an info message when --strip-xattrs leaves it out.
func TestStripLogsInfoOnlyForCapability(t *testing.T) {
	SetStripXattrs(true)
	t.Cleanup(func() { SetStripXattrs(false) })
	oldLevel := logrus.GetLevel()
	logrus.SetLevel(logrus.DebugLevel)
	t.Cleanup(func() { logrus.SetLevel(oldLevel) })

	for _, tc := range []struct {
		name     string
		pax      map[string]string
		wantInfo int
	}{
		{"capability", map[string]string{"SCHILY.xattr.security.capability": capBlob}, 1},
		{"capability-and-others", map[string]string{"SCHILY.xattr.security.capability": capBlob, "SCHILY.xattr.user.x": "v"}, 1},
		{"acl-only", map[string]string{"SCHILY.acl.access": "user::rw-"}, 0},
		{"user-xattr-only", map[string]string{"SCHILY.xattr.user.x": "v"}, 0},
		{"none", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hook := &captureHook{}
			logrus.AddHook(hook)
			t.Cleanup(func() { logrus.StandardLogger().ReplaceHooks(make(logrus.LevelHooks)) })

			hdr := &tar.Header{Typeflag: tar.TypeReg, Name: "f", PAXRecords: tc.pax}
			if err := writeSecurityXattrToTarFile("/nonexistent/f", hdr); err != nil {
				t.Fatal(err)
			}
			info := 0
			for _, e := range hook.entries {
				if e.Level == logrus.InfoLevel {
					info++
				}
			}
			if info != tc.wantInfo {
				t.Errorf("got %d info messages, want %d: %v", info, tc.wantInfo, hook.entries)
			}
		})
	}
}
