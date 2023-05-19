package ocimem

import (
	"fmt"
	"regexp"
	"sync"

	"github.com/opencontainers/go-digest"
	"github.com/rogpeppe/ociregistry"
)

type Registry struct {
	mu    sync.Mutex
	repos map[string]*repository
}

var _ ociregistry.Interface = (*Registry)(nil)

type repository struct {
	tags      map[string]ociregistry.Descriptor
	manifests map[ociregistry.Digest]*blob
	blobs     map[ociregistry.Digest]*blob
}

type blob struct {
	mediaType string
	data      []byte
}

func (b *blob) descriptor() ociregistry.Descriptor {
	return ociregistry.Descriptor{
		MediaType: b.mediaType,
		Size:      int64(len(b.data)),
		Digest:    digest.FromBytes(b.data),
	}
}

func New() *Registry {
	return &Registry{}
}

var noRepo = new(repository)

func (r *Registry) repo(repoName string) *repository {
	if repo, ok := r.repos[repoName]; ok {
		return repo
	}
	return noRepo
}

func (r *Registry) makeRepo(repoName string) (*repository, error) {
	if !isValidRepoName(repoName) {
		return nil, fmt.Errorf("invalid repository name %q", repoName)
	}
	if r.repos == nil {
		r.repos = make(map[string]*repository)
	}
	if repo := r.repos[repoName]; repo != nil {
		return repo, nil
	}
	repo := &repository{
		tags:      make(map[string]ociregistry.Descriptor),
		manifests: make(map[digest.Digest]*blob),
		blobs:     make(map[digest.Digest]*blob),
	}
	r.repos[repoName] = repo
	return repo, nil
}

var (
	tagPattern      = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}$`)
	repoNamePattern = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*(/[a-z0-9]+([._-][a-z0-9]+)*)*$`)
)

func isValidRepoName(repoName string) bool {
	return repoNamePattern.MatchString(repoName)
}

func isValidTag(tag string) bool {
	return tagPattern.MatchString(tag)
}

// CheckDescriptor checks that the given descriptor matches the given data or,
// if data is nil, that the descriptor looks sane.
func CheckDescriptor(desc ociregistry.Descriptor, data []byte) error {
	if data != nil {
		if digest.FromBytes(data) != desc.Digest {
			return fmt.Errorf("digest mismatch")
		}
		if desc.Size != int64(len(data)) {
			return fmt.Errorf("size mismatch")
		}
	} else {
		if desc.Size == 0 {
			return fmt.Errorf("zero sized content")
		}
	}
	if desc.MediaType == "" {
		return fmt.Errorf("no media type in descriptor")
	}
	return nil
}