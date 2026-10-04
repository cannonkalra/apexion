package s3

import (
	"github.com/apexion/apexion/internal/features"
	"github.com/apexion/apexion/internal/objstore"
)

// connector builds S3-compatible ObjectStores. It is the only bridge between a
// connection Config and the AWS SDK; the rest of the app calls it through the
// feature registry and receives an objstore.ObjectStore.
type connector struct{}

func (connector) Connect(cfg objstore.Config) (objstore.ObjectStore, error) {
	return newStore(cfg), nil
}

// The S3-compatible client is the core object-storage backend. MinIO, SeaweedFS
// and AWS S3 are all served by this one client — they differ only in endpoint,
// TLS and addressing configuration — so they are registered as aliases of "s3".
// Azure and GCS are not S3-compatible and will register their own descriptors
// from build-tagged packages when implemented.
func init() {
	features.RegisterStorageProvider(features.StorageProvider{
		Name:      "s3",
		Aliases:   []string{"minio", "seaweed", "seaweedfs", "aws"},
		Kind:      "object",
		Connector: connector{},
	})
}
