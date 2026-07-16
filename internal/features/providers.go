package features

import (
	"sort"

	"github.com/apexion/apexion/internal/objstore"
)

// StorageProvider describes a storage backend that the Explorer and Crawler can
// read from. It is metadata only — the concrete client lives in the provider's
// own package and registers this descriptor from its init(). Optional backends
// (Azure, GCS) carry a BuildTag and register from a build-tagged file, so they
// are absent from the binary unless compiled in.
//
// The S3-compatible client (package internal/objstore/s3) backs the core
// "s3" provider and its aliases (MinIO, SeaweedFS, AWS): they differ only in
// endpoint/TLS/addressing configuration, not in implementation.
type StorageProvider struct {
	Name      string             // canonical id, e.g. "s3"
	Aliases   []string           // accepted alternative names, e.g. minio, seaweed, aws
	Kind      string             // "object" (bucket/key) or "file" (path)
	BuildTag  string             // "" for core; e.g. "azure" for tag-gated backends
	Connector objstore.Connector // builds a connected ObjectStore from a Config
}

var storageProviders = map[string]StorageProvider{}

// RegisterStorageProvider adds a storage backend descriptor. Called from a
// provider package's init(). Aliases are indexed so lookups resolve them.
func RegisterStorageProvider(p StorageProvider) {
	storageProviders[p.Name] = p
	for _, a := range p.Aliases {
		if _, taken := storageProviders[a]; !taken {
			storageProviders[a] = p
		}
	}
}

// StorageProvider resolves a provider by canonical name or alias. ok is false
// when no such backend was compiled in.
func LookupStorageProvider(name string) (StorageProvider, bool) {
	p, ok := storageProviders[name]
	return p, ok
}

// StorageProviders lists the distinct storage backends compiled into this
// binary, by canonical name, sorted.
func StorageProviders() []StorageProvider {
	seen := map[string]bool{}
	var out []StorageProvider
	for _, p := range storageProviders {
		if seen[p.Name] {
			continue
		}
		seen[p.Name] = true
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
