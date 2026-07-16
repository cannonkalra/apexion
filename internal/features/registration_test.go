package features_test

import (
	"testing"

	"github.com/apexion/apexion/internal/features"
	"github.com/apexion/apexion/internal/model"

	// Pull in the compiled-in capabilities exactly as the binary does, so this
	// test exercises the real self-registration path. This single blank import
	// serves every test file in the package.
	_ "github.com/apexion/apexion/internal/plugins"
)

// TestCoreReadersRegistered verifies the core formats self-register in every
// build, regardless of tags.
func TestCoreReadersRegistered(t *testing.T) {
	core := []model.Format{
		model.FormatCSV, model.FormatTSV,
		model.FormatJSON, model.FormatJSONL,
		model.FormatParquet,
	}
	for _, f := range core {
		if features.Reader(f) == nil {
			t.Errorf("core format %q not registered in default build", f)
		}
	}
}
