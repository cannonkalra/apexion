package s3

import (
	"testing"

	"github.com/apexion/apexion/internal/objstore"
)

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
	yes := []objstore.Config{
		{Provider: "aws", Endpoint: ""},
		{Provider: "minio", Endpoint: "s3.amazonaws.com"},
		{Provider: "", Endpoint: "bucket.s3.us-east-1.amazonaws.com"},
	}
	for _, c := range yes {
		if !isAWS(c) {
			t.Errorf("isAWS(%q,%q) = false, want true", c.Provider, c.Endpoint)
		}
	}
	if isAWS(objstore.Config{Provider: "minio", Endpoint: "localhost:9000"}) {
		t.Error("isAWS(minio, localhost) = true, want false")
	}
}
