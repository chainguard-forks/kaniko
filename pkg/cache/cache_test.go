package cache

import (
	"testing"

	"github.com/chainguard-dev/kaniko/pkg/config"
	"github.com/google/go-containerregistry/pkg/name"
)

func TestDestination(t *testing.T) {
	tests := []struct {
		name      string
		cacheRepo string
		cacheKey  string
		wantErr   bool
	}{
		{
			name:      "repo with port",
			cacheRepo: "localhost:5000/myrepo",
			cacheKey:  "abc",
			wantErr:   false,
		},
		{
			name:      "repo with port and host",
			cacheRepo: "host.docker.internal:6000/myrepo",
			cacheKey:  "abc",
			wantErr:   false,
		},
		{
			name:      "root repo with port",
			cacheRepo: "localhost:5100",
			cacheKey:  "abc",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &config.KanikoOptions{
				CacheRepo: tt.cacheRepo,
			}
			got, err := Destination(opts, tt.cacheKey)
			if err != nil {
				t.Errorf("Destination() error = %v", err)
				return
			}

			// Validate the result can be parsed by name.NewTag
			_, err = name.NewTag(got, name.WeakValidation)
			if (err != nil) != tt.wantErr {
				t.Errorf("name.NewTag(%q) error = %v, wantErr %v", got, err, tt.wantErr)
			}
		})
	}
}

func TestDestinationInsecure(t *testing.T) {
	tests := []struct {
		name      string
		cacheRepo string
		cacheKey  string
		insecure  bool
		wantErr   bool
	}{
		{
			name:      "insecure repo with port",
			cacheRepo: "localhost:5100",
			cacheKey:  "abc",
			insecure:  true,
			wantErr:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &config.KanikoOptions{
				CacheRepo: tt.cacheRepo,
			}
			opts.Insecure = tt.insecure
			got, err := Destination(opts, tt.cacheKey)
			if err != nil {
				t.Errorf("Destination() error = %v", err)
				return
			}

			// Validate the result can be parsed by name.NewTag
			_, err = name.NewTag(got, name.WeakValidation)
			if (err != nil) != tt.wantErr {
				t.Errorf("name.NewTag(%q) error = %v, wantErr %v", got, err, tt.wantErr)
			}
		})
	}
}
