//go:build orc

package orc

import "github.com/apexion/apexion/internal/features"

// ORC is an optional format: compiled and registered only under -tags orc.
func init() { features.RegisterReader(New()) }
