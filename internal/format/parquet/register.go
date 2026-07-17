package parquet

import "github.com/apexion/apexion/internal/features"

// Parquet is a core format: always compiled, always registered.
func init() { features.RegisterReader(New()) }
