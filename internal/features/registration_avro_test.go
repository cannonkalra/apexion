//go:build avro

package features_test

import (
	"testing"

	"github.com/apexion/apexion/internal/features"
	"github.com/apexion/apexion/internal/model"
)

func TestAvroRegisteredUnderTag(t *testing.T) {
	if features.Reader(model.FormatAvro) == nil {
		t.Error("avro reader not registered under -tags avro")
	}
}
