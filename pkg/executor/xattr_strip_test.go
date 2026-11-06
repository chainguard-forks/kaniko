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

package executor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chainguard-dev/kaniko/pkg/config"
	"github.com/chainguard-dev/kaniko/pkg/util"
)

// DoBuild has to set --strip-xattrs before it extracts anything: fetchExtraStages
// unpacks the images of COPY --from=<image> before the first stage builder
// exists. The build here fails on its Dockerfile, which is after the flag is set
// and before anything is extracted.
func TestDoBuildSetsStripXattrsFirstAndClearsOldLayers(t *testing.T) {
	oldDir := config.KanikoDir
	config.KanikoDir = t.TempDir()
	t.Cleanup(func() {
		config.KanikoDir = oldDir
		util.SetStripXattrs(false)
	})

	// A rewritten layer left by an earlier run in a reused container.
	stale := filepath.Join(config.KanikoDir, "xattr-strip", "layer-old.tar")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, want := range []bool{true, false} {
		util.SetStripXattrs(!want)
		opts := &config.KanikoOptions{
			DockerfilePath: filepath.Join(t.TempDir(), "no-such-Dockerfile"),
			SrcContext:     t.TempDir(),
			StripXattrs:    want,
		}
		if _, err := DoBuild(opts); err == nil {
			t.Fatal("expected the build to fail on the missing Dockerfile")
		}
		if got := util.StripXattrs(); got != want {
			t.Errorf("after DoBuild with StripXattrs=%v the setting is %v", want, got)
		}
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("layer from an earlier run was not removed: %v", err)
	}
}
