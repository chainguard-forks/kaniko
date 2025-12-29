package cache

import (
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
)

func TestNameParsing(t *testing.T) {
	inputs := []string{
		"localhost:5000",
		"localhost:5000/repo",
		"gcr.io/proj",
		"host.docker.internal:6000",
	}

	for _, input := range inputs {
		repo, err := name.NewRepository(input, name.WeakValidation)
		if err != nil {
			t.Logf("NewRepository(%q) error: %v", input, err)
		} else {
			t.Logf("NewRepository(%q) success. Registry=%s, Repository=%s, Name=%s", input, repo.Registry.Name(), repo.RepositoryStr(), repo.Name())
		}
	}
}
