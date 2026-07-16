package crawler

import (
	"github.com/apexion/apexion/internal/features"
	"github.com/apexion/apexion/internal/format"
	"github.com/apexion/apexion/internal/model"
)

// DefaultRegistry returns the file-format reader registry assembled from every
// reader compiled into this binary. Readers register themselves with package
// features via their init(); the crawler never imports a concrete format.
// The set of readers present therefore depends purely on build tags.
func DefaultRegistry() *format.Registry { return features.ReaderRegistry() }

// DefaultResolvers returns the table-format resolvers (Iceberg, Delta, ...)
// compiled into this binary, likewise sourced from the feature registry.
func DefaultResolvers() map[model.Format]format.TableResolver { return features.Resolvers() }
