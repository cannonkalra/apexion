// Package plugins is the single place that pulls compiled-in capabilities into
// the build. It contains nothing but blank imports: importing a reader package
// runs its init(), which self-registers with package features.
//
// This is deliberately separate from package features to keep the dependency
// graph acyclic: implementations import features; plugins imports the
// implementations; features imports neither. The main binary blank-imports
// this package once (see internal/app) so every registration fires before the
// application graph is built.
//
// Core capabilities are imported here unconditionally. Each optional capability
// lives in its own build-tagged file (avro.go, orc.go, iceberg.go, delta.go);
// a capability absent from the build tags is never imported, so its code and
// its third-party dependencies never enter the binary.
package plugins

import (
	// Core file-format readers — always present.
	_ "github.com/apexion/apexion/internal/format/csv"
	_ "github.com/apexion/apexion/internal/format/json"
	_ "github.com/apexion/apexion/internal/format/parquet"

	// Core object-storage backend (S3-compatible: S3/MinIO/SeaweedFS/AWS).
	_ "github.com/apexion/apexion/internal/objstore/s3"
)
