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
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/chainguard-dev/kaniko/pkg/config"
	"github.com/google/go-containerregistry/pkg/compression"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/sirupsen/logrus"
)

// Layer tar entries can carry extended attributes as PAX records. Docker,
// BuildKit and containerd apply every SCHILY.xattr.* record when they unpack a
// layer, so a base image can hide file capabilities (security.capability) or
// POSIX ACLs (system.posix_acl_access) in a layer without any sign in the
// mode bits or a default `tar -tv` listing. SCHILY.acl.* is the record GNU
// tar and bsdtar use for ACLs.
//
// By default kaniko behaves like those builders and keeps the records.
// --strip-xattrs drops them, both for files kaniko writes itself and for the
// layers of the base image, which it would otherwise push unchanged.
const (
	paxXattrPrefix = "SCHILY.xattr."
	paxACLPrefix   = "SCHILY.acl."

	// xattrStripDirName is the directory under the kaniko directory that holds
	// the rewritten base image layers until the image has been pushed.
	xattrStripDirName = "xattr-strip"
)

var stripXattrs atomic.Bool

// SetStripXattrs controls whether extended attributes from layer tar entries
// are dropped (true) or kept (false, the default).
func SetStripXattrs(v bool) { stripXattrs.Store(v) }

// StripXattrs reports whether extended attributes are dropped.
func StripXattrs() bool { return stripXattrs.Load() }

func xattrStripDir() string { return filepath.Join(config.KanikoDir, xattrStripDirName) }

// RemoveXattrStripFiles deletes the rewritten layers that StripImageXattrs
// wrote. They have to stay in place until the image has been pushed, so this is
// called at the start of a build (for files left by an earlier run in a reused
// container) and again after the push.
func RemoveXattrStripFiles() error { return os.RemoveAll(xattrStripDir()) }

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

// rewrittenMediaType returns the media type for a layer that stripLayerXattrs
// has rewritten. go-containerregistry always compresses the blobs it builds
// (WithCompression(None) is not supported), so an uncompressed layer type would
// describe a gzip blob; use the matching compressed type instead.
func rewrittenMediaType(mt types.MediaType) types.MediaType {
	switch mt {
	case types.OCIUncompressedLayer:
		return types.OCILayer
	case types.DockerUncompressedLayer:
		return types.DockerLayer
	}
	return mt
}

// stripLayerXattrs returns a copy of l with every xattr and ACL record removed
// from its tar entries, and the number of entries that had records. File
// contents, modes, ownership, times and entry order are unchanged. If no entry
// has a record it returns a nil layer and nothing is kept.
//
// The layer is read once: it is rewritten into a temporary file while it is
// scanned, and the file is discarded if nothing was stripped. The file is under
// the kaniko directory (which is excluded from snapshots) so that a large layer
// is not held in memory, and stays there until RemoveXattrStripFiles because the
// returned layer reads from it when the image is pushed. The rewritten blob is
// compressed with compressionLevel, like the layers kaniko builds itself.
func stripLayerXattrs(l v1.Layer, compressionLevel int) (v1.Layer, int, error) {
	mt, err := l.MediaType()
	if err != nil {
		return nil, 0, err
	}
	opts := []tarball.LayerOption{tarball.WithMediaType(rewrittenMediaType(mt))}
	if strings.Contains(string(mt), "zstd") {
		opts = append(opts, tarball.WithCompression(compression.ZStd))
	}
	if compressionLevel > 0 {
		opts = append(opts, tarball.WithCompressionLevel(compressionLevel))
	}

	r, err := l.Uncompressed()
	if err != nil {
		return nil, 0, err
	}
	defer r.Close()

	dir := xattrStripDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, 0, err
	}
	out, err := os.CreateTemp(dir, "layer-*.tar")
	if err != nil {
		return nil, 0, err
	}
	keep := false
	defer func() {
		out.Close()
		if !keep {
			os.Remove(out.Name())
		}
	}()

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
	// Drain the trailing bytes so the digest of the source layer is verified.
	if _, err := io.Copy(io.Discard, r); err != nil {
		return nil, 0, fmt.Errorf("verifying layer: %w", err)
	}
	if stripped == 0 {
		return nil, 0, nil
	}
	if err := tw.Close(); err != nil {
		return nil, 0, err
	}
	if err := out.Sync(); err != nil {
		return nil, 0, err
	}

	nl, err := tarball.LayerFromFile(out.Name(), opts...)
	if err != nil {
		return nil, 0, err
	}
	keep = true
	return nl, stripped, nil
}

// StripImageXattrs returns img with the xattr and ACL records removed from
// every layer that has them. Layers without such records are kept as they
// are, with their digests. If no layer has any record the original image is
// returned, so the image digest is unchanged.
//
// Finding the affected layers means reading every layer once. For an image
// that has to be pulled that is an extra download of each layer. A base layer
// that Go's archive/tar cannot parse makes this fail, including for builds that
// would never have unpacked that layer. Rewritten layers are compressed with
// compressionLevel (the --compression-level value, 0 for the default).
func StripImageXattrs(img v1.Image, compressionLevel int) (v1.Image, error) {
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
		nl, n, err := stripLayerXattrs(l, compressionLevel)
		if err != nil {
			return nil, fmt.Errorf("removing extended attributes from layer %d: %w", i, err)
		}
		if nl == nil {
			newLayers[i] = l
			continue
		}
		d, _ := l.Digest()
		nd, _ := nl.Digest()
		logrus.Infof("Removed extended attributes (file capabilities, ACLs) from %d entries in base image layer %d (%s -> %s)", n, i, d, nd)
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
