package features_test

import (
	"testing"

	"github.com/apexion/apexion/internal/features"
)

// TestS3StorageProviderRegistered verifies the core object-storage backend and
// its aliases resolve through the storage-provider registry.
func TestS3StorageProviderRegistered(t *testing.T) {
	for _, name := range []string{"s3", "minio", "seaweed", "aws"} {
		p, ok := features.LookupStorageProvider(name)
		if !ok {
			t.Errorf("storage provider %q not registered", name)
			continue
		}
		if p.Name != "s3" || p.Kind != "object" {
			t.Errorf("provider %q resolved to unexpected descriptor %+v", name, p)
		}
	}
	if len(features.StorageProviders()) == 0 {
		t.Error("no storage providers registered")
	}
}
