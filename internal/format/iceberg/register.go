//go:build iceberg

package iceberg

import "github.com/apexion/apexion/internal/features"

// Iceberg is an optional table format: compiled and registered only under
// -tags iceberg.
func init() { features.RegisterResolver(New()) }
