//go:build delta

package features_test

import (
	"testing"

	"github.com/apexion/apexion/internal/features"
	"github.com/apexion/apexion/internal/model"
)

func TestDeltaRegisteredUnderTag(t *testing.T) {
	if _, ok := features.Resolvers()[model.FormatDelta]; !ok {
		t.Error("delta resolver not registered under -tags delta")
	}
}
