//go:build delta

package delta

import "github.com/apexion/apexion/internal/features"

// Delta is an optional table format: compiled and registered only under
// -tags delta.
func init() { features.RegisterResolver(New()) }
