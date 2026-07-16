//go:build !avro && !orc && !iceberg && !delta

package features_test

import (
	"testing"

	"github.com/apexion/apexion/internal/features"
	"github.com/apexion/apexion/internal/model"
)

// TestOptionalReadersAbsentByDefault guards the compile-time boundary: with no
// optional tags set, none of the optional capabilities may be present.
func TestOptionalReadersAbsentByDefault(t *testing.T) {
	for _, f := range []model.Format{model.FormatAvro, model.FormatORC} {
		if features.Reader(f) != nil {
			t.Errorf("optional format %q registered without its build tag", f)
		}
	}
	if _, ok := features.Resolvers()[model.FormatIceberg]; ok {
		t.Error("iceberg resolver registered without its build tag")
	}
	if _, ok := features.Resolvers()[model.FormatDelta]; ok {
		t.Error("delta resolver registered without its build tag")
	}
}
