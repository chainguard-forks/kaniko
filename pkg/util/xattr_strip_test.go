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

func buildLayer(t *testing.T, entries []tarEntry) v1.Layer {
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
	})
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

	got, err := StripImageXattrs(img)
	if err != nil {
		t.Fatal(err)
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
	got, err := StripImageXattrs(img)
	if err != nil {
		t.Fatal(err)
	}
	if got != img {
		t.Error("image without xattrs was rebuilt")
	}
	od, _ := img.Digest()
	nd, _ := got.Digest()
	if od != nd {
		t.Error("digest changed")
	}
}

func TestExtractFileDropsXattrsByDefault(t *testing.T) {
	for _, preserve := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "preserve"}[preserve], func(t *testing.T) {
			if preserve && os.Geteuid() != 0 {
				t.Skip("setting security.capability needs root")
			}
			SetPreserveXattrs(preserve)
			t.Cleanup(func() { SetPreserveXattrs(false) })

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
			if !preserve {
				if got != nil {
					t.Fatalf("capability was written by default: %q", got)
				}
				return
			}
			if err != nil || got == nil {
				t.Fatalf("capability was not kept with --preserve-xattrs: %v", err)
			}
		})
	}
}
