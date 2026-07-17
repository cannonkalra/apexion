//go:build avro

package avro

import "github.com/apexion/apexion/internal/features"

// Avro is an optional format: compiled and registered only under -tags avro.
func init() { features.RegisterReader(New()) }
