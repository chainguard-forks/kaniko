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
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"

	"github.com/chainguard-dev/kaniko/pkg/config"
	"github.com/google/go-containerregistry/pkg/compression"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/sirupsen/logrus"
)

// Layer tar entries can carry extended attributes as PAX records. Docker,
// BuildKit and containerd apply every SCHILY.xattr.* record when they unpack a
// layer, so a base image can hide file capabilities (security.capability) or
// POSIX ACLs (system.posix_acl_access) in a layer without any sign in the
// mode bits or a default `tar -tv` listing. SCHILY.acl.* is the record GNU
// tar and bsdtar use for ACLs.
//
// kaniko drops these records by default, both for files it writes itself and
// for the layers of the base image, which it would otherwise push unchanged.
// --preserve-xattrs turns that off.
const (
	paxXattrPrefix = "SCHILY.xattr."
	paxACLPrefix   = "SCHILY.acl."
)

var preserveXattrs atomic.Bool

// SetPreserveXattrs controls whether extended attributes from layer tar
// entries are kept (true) or dropped (false, the default).
func SetPreserveXattrs(v bool) { preserveXattrs.Store(v) }

// PreserveXattrs reports whether extended attributes are kept.
func PreserveXattrs() bool { return preserveXattrs.Load() }

func hasKey(m map[string]string, k string) bool {
	_, ok := m[k]
	return ok
}

func isXattrRecord(key string) bool {
	return strings.HasPrefix(key, paxXattrPrefix) || strings.HasPrefix(key, paxACLPrefix)
}

// xattrRecordNames returns the names of the xattr/ACL records on hdr, in the
// form they appear in the tar (for example "SCHILY.xattr.security.capability").
func xattrRecordNames(hdr *tar.Header) []string {
	var names []string
	for k := range hdr.PAXRecords {
		if isXattrRecord(k) {
			names = append(names, k)
		}
	}
	// hdr.Xattrs is deprecated but is still filled in by the reader and honoured
	// by the writer, so it has to be checked and cleared as well.
	for k := range hdr.Xattrs { //nolint:staticcheck
		if name := paxXattrPrefix + k; !hasKey(hdr.PAXRecords, name) {
			names = append(names, name)
		}
	}
	return names
}

// stripXattrRecords removes every xattr and ACL record from hdr and returns
// the names it removed.
func stripXattrRecords(hdr *tar.Header) []string {
	names := xattrRecordNames(hdr)
	for _, n := range names {
		delete(hdr.PAXRecords, n)
	}
	hdr.Xattrs = nil //nolint:staticcheck
	if len(names) > 0 {
		// Let the writer pick the format: the original may have been PAX only
		// because of these records.
		hdr.Format = tar.FormatUnknown
	}
	return names
}

// layerHasXattrs reports whether any entry of the layer carries xattr or ACL
// records.
func layerHasXattrs(l v1.Layer) (bool, error) {
	r, err := l.Uncompressed()
	if err != nil {
		return false, err
	}
	defer r.Close()

	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			// Drain the trailing bytes so the digest is verified.
			_, err := io.Copy(io.Discard, r)
			return false, err
		}
		if err != nil {
			return false, err
		}
		if len(xattrRecordNames(hdr)) > 0 {
			return true, nil
		}
	}
}

// stripLayerXattrs returns a copy of l with every xattr and ACL record removed
// from its tar entries. File contents, modes, ownership, times and entry order
// are unchanged. The new layer is written to a file under the kaniko directory
// (which is excluded from snapshots) so that a large layer is not held in memory.
func stripLayerXattrs(l v1.Layer) (v1.Layer, int, error) {
	mt, err := l.MediaType()
	if err != nil {
		return nil, 0, err
	}
	opts := []tarball.LayerOption{tarball.WithMediaType(mt)}
	if strings.Contains(string(mt), "zstd") {
		opts = append(opts, tarball.WithCompression(compression.ZStd))
	}

	r, err := l.Uncompressed()
	if err != nil {
		return nil, 0, err
	}
	defer r.Close()

	if err := os.MkdirAll(config.KanikoDir, 0o755); err != nil {
		return nil, 0, err
	}
	out, err := os.CreateTemp(config.KanikoDir, "xattr-strip-*.tar")
	if err != nil {
		return nil, 0, err
	}
	defer out.Close()

	tw := tar.NewWriter(out)
	tr := tar.NewReader(r)
	stripped := 0
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, 0, fmt.Errorf("reading layer: %w", err)
		}
		if len(stripXattrRecords(hdr)) > 0 {
			stripped++
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, 0, fmt.Errorf("writing tar header for %q: %w", hdr.Name, err)
		}
		if hdr.Size > 0 {
			if _, err := io.CopyN(tw, tr, hdr.Size); err != nil {
				return nil, 0, fmt.Errorf("copying %q: %w", hdr.Name, err)
			}
		}
	}
	if _, err := io.Copy(io.Discard, r); err != nil {
		return nil, 0, fmt.Errorf("verifying layer: %w", err)
	}
	if err := tw.Close(); err != nil {
		return nil, 0, err
	}
	if err := out.Sync(); err != nil {
		return nil, 0, err
	}

	nl, err := tarball.LayerFromFile(out.Name(), opts...)
	return nl, stripped, err
}

// StripImageXattrs returns img with the xattr and ACL records removed from
// every layer that has them. Layers without such records are kept as they
// are, with their digests. If no layer has any record the original image is
// returned, so the image digest is unchanged.
//
// Finding the affected layers means reading every layer once. For an image
// that has to be pulled that is an extra download of each layer.
func StripImageXattrs(img v1.Image) (v1.Image, error) {
	layers, err := img.Layers()
	if err != nil {
		return nil, err
	}

	newLayers := make([]v1.Layer, len(layers))
	changed := false
	for i, l := range layers {
		mt, err := l.MediaType()
		if err != nil {
			return nil, err
		}
		if !mt.IsLayer() || !mt.IsDistributable() {
			// Foreign layers are not ours to rewrite.
			newLayers[i] = l
			continue
		}
		has, err := layerHasXattrs(l)
		if err != nil {
			return nil, fmt.Errorf("scanning layer %d for extended attributes: %w", i, err)
		}
		if !has {
			newLayers[i] = l
			continue
		}
		nl, n, err := stripLayerXattrs(l)
		if err != nil {
			return nil, fmt.Errorf("removing extended attributes from layer %d: %w", i, err)
		}
		d, _ := l.Digest()
		nd, _ := nl.Digest()
		logrus.Infof("Dropped extended attributes (file capabilities, ACLs) from %d entries in base image layer %d (%s -> %s); use --preserve-xattrs to keep them", n, i, d, nd)
		newLayers[i] = nl
		changed = true
	}
	if !changed {
		return img, nil
	}

	ocf, err := img.ConfigFile()
	if err != nil {
		return nil, err
	}
	manifest, err := img.Manifest()
	if err != nil {
		return nil, err
	}
	imgMediaType, err := img.MediaType()
	if err != nil {
		return nil, err
	}

	out := mutate.MediaType(empty.Image, imgMediaType)
	if manifest.Config.MediaType != "" {
		out = mutate.ConfigMediaType(out, manifest.Config.MediaType)
	}
	addendums := make([]mutate.Addendum, len(newLayers))
	for i, l := range newLayers {
		addendums[i] = mutate.Addendum{Layer: l}
	}
	out, err = mutate.Append(out, addendums...)
	if err != nil {
		return nil, fmt.Errorf("appending layers: %w", err)
	}

	// Keep the original config, history included. Only the diff IDs change.
	nf, err := out.ConfigFile()
	if err != nil {
		return nil, err
	}
	cfg := ocf.DeepCopy()
	cfg.RootFS = nf.RootFS
	out, err = mutate.ConfigFile(out, cfg)
	if err != nil {
		return nil, err
	}
	if manifest.Annotations != nil {
		out = mutate.Annotations(out, manifest.Annotations).(v1.Image)
	}
	return out, nil
}
