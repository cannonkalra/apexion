//go:build iceberg

package features_test

import (
	"testing"

	"github.com/apexion/apexion/internal/features"
	"github.com/apexion/apexion/internal/model"
)

func TestIcebergRegisteredUnderTag(t *testing.T) {
	if _, ok := features.Resolvers()[model.FormatIceberg]; !ok {
		t.Error("iceberg resolver not registered under -tags iceberg")
	}
}
