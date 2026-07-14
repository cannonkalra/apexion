package connections

import (
	"testing"

	"github.com/apexion/apexion/internal/model"
)

func modelConn(provider, endpoint string) *model.Connection {
	return &model.Connection{Provider: provider, Endpoint: endpoint}
}

func TestNeedsPathStyle(t *testing.T) {
	cases := map[string]bool{
		"eyeota-data-feed": false, // DNS-compliant → virtual-host
		"warehouse":        false,
		"my.bucket":        true, // dot breaks virtual-host + TLS
		"Dharani_test":     true, // uppercase + underscore
		"UPPER":            true,
		"has_underscore":   true,
		"":                 false,
	}
	for bucket, want := range cases {
		if got := needsPathStyle(bucket); got != want {
			t.Errorf("needsPathStyle(%q) = %v, want %v", bucket, got, want)
		}
	}
}

func TestIsAWS(t *testing.T) {
	yes := []struct{ provider, endpoint string }{
		{"aws", ""},
		{"minio", "s3.amazonaws.com"},
		{"", "bucket.s3.us-east-1.amazonaws.com"},
	}
	for _, c := range yes {
		if !isAWS(modelConn(c.provider, c.endpoint)) {
			t.Errorf("isAWS(%q,%q) = false, want true", c.provider, c.endpoint)
		}
	}
	if isAWS(modelConn("minio", "localhost:9000")) {
		t.Error("isAWS(minio, localhost) = true, want false")
	}
}
