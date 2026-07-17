//go:build orc

package features_test

import (
	"testing"

	"github.com/apexion/apexion/internal/features"
	"github.com/apexion/apexion/internal/model"
)

func TestORCRegisteredUnderTag(t *testing.T) {
	if features.Reader(model.FormatORC) == nil {
		t.Error("orc reader not registered under -tags orc")
	}
}
