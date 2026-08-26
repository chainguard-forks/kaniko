/*
Copyright 2026 Chainguard, Inc.

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
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

// ggcrDefaultJobs mirrors go-containerregistry's remote.defaultJobs, the size
// of the pullLimiter slot pool.
const ggcrDefaultJobs = 4

// limiterLayer models the pullLimiter that go-containerregistry v0.21.6 and
// later put in front of remote blob fetches: Uncompressed() takes a slot from a
// fixed-size pool and blocks, with no deadline, once the pool is exhausted. The
// slot is only returned when the reader is closed.
//
// GetFSFromLayers used to defer every layer's Close() until the whole image had
// been unpacked, so the slots leaked and the fifth layer blocked forever. This
// fake reproduces that deadlock without needing a registry.
type limiterLayer struct {
	v1.Layer // only MediaType and Uncompressed are exercised
	tokens   chan struct{}
	body     []byte
}

func (l *limiterLayer) MediaType() (types.MediaType, error) { return types.OCILayer, nil }

func (l *limiterLayer) Uncompressed() (io.ReadCloser, error) {
	l.tokens <- struct{}{}
	return &limiterReadCloser{Reader: bytes.NewReader(l.body), tokens: l.tokens}, nil
}

type limiterReadCloser struct {
	io.Reader
	tokens chan struct{}
	once   sync.Once
}

func (r *limiterReadCloser) Close() error {
	r.once.Do(func() { <-r.tokens })
	return nil
}

func Test_GetFSFromLayers_releases_reader_before_next_layer(t *testing.T) {
	resetMountInfoFile := provideEmptyMountinfoFile()
	defer resetMountInfoFile()

	root := t.TempDir()

	// More layers than the limiter has slots, so a leak is guaranteed to block.
	const numLayers = ggcrDefaultJobs * 3
	tokens := make(chan struct{}, ggcrDefaultJobs)

	layers := make([]v1.Layer, 0, numLayers)
	expectedFiles := make([]string, 0, numLayers)
	for i := 0; i < numLayers; i++ {
		name := fmt.Sprintf("file-%d", i)

		buf := new(bytes.Buffer)
		tw := tar.NewWriter(buf)
		if err := tw.WriteHeader(&tar.Header{
			Name:     name,
			Typeflag: tar.TypeReg,
			Mode:     0o644,
		}); err != nil {
			t.Fatalf("writing tar header: %s", err)
		}
		if err := tw.Close(); err != nil {
			t.Fatalf("closing tar writer: %s", err)
		}

		layers = append(layers, &limiterLayer{tokens: tokens, body: buf.Bytes()})
		expectedFiles = append(expectedFiles, filepath.Join(root, name))
	}

	opts := []FSOpt{ExtractFunc(func(string, *tar.Header, string, io.Reader) error { return nil })}

	type result struct {
		files []string
		err   error
	}
	done := make(chan result, 1)
	go func() {
		files, err := GetFSFromLayers(root, layers, opts...)
		done <- result{files, err}
	}()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("GetFSFromLayers returned an error: %s", got.err)
		}
		if len(got.files) != len(expectedFiles) {
			t.Fatalf("expected %d extracted files, got %d", len(expectedFiles), len(got.files))
		}
	case <-time.After(30 * time.Second):
		t.Fatal("GetFSFromLayers deadlocked: each layer's reader must be closed before the next layer is opened, or the go-containerregistry pullLimiter runs out of slots")
	}

	if len(tokens) != 0 {
		t.Errorf("expected all %d limiter slots to be released, %d still held", ggcrDefaultJobs, len(tokens))
	}
}
