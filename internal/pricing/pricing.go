// Package pricing estimates the monthly S3 storage cost of objects from their
// size and storage class, using AWS's published on-demand list prices
// (rates_gen.go, regenerated from the AWS Price List API).
//
// It covers storage only — not requests, retrieval, transfer, or
// Intelligent-Tiering monitoring fees — and uses each class's first volume tier
// (the first 50 TB/month for Standard), since the tier depends on the whole
// account's usage, which a single object or folder cannot know.
package pricing

//go:generate go run gen/main.go

import "strings"

// DefaultRegion prices objects whose region has no published S3 rates, such as
// MinIO, SeaweedFS, or R2 endpoints (their cost is shown as the S3 equivalent).
const DefaultRegion = "us-east-1"

// Storage classes as S3 reports them in object listings.
const (
	Standard           = "STANDARD"
	StandardIA         = "STANDARD_IA"
	OneZoneIA          = "ONEZONE_IA"
	GlacierIR          = "GLACIER_IR"
	Glacier            = "GLACIER"
	DeepArchive        = "DEEP_ARCHIVE"
	IntelligentTiering = "INTELLIGENT_TIERING"
)

const (
	gb = 1 << 30 // AWS bills storage in binary gigabytes

	// minBillable is the minimum billable object size for the IA and Glacier
	// Instant Retrieval classes: smaller objects are charged as 128 KB.
	minBillable = 128 << 10
	// Glacier Flexible Retrieval and Deep Archive add per-object overhead:
	// 32 KB at the class rate (index) plus 8 KB at the Standard rate (name).
	archiveOverhead  = 32 << 10
	standardOverhead = 8 << 10
)

// Region resolves the pricing region for a bucket region: the region itself
// when AWS publishes S3 rates for it, otherwise DefaultRegion.
func Region(region string) string {
	if _, ok := storageRates[region]; ok {
		return region
	}
	return DefaultRegion
}

// Rate returns the storage rate, in USD per GB-month, of a storage class in a
// pricing region (see Region). An empty or unpriced class (e.g. OUTPOSTS) is
// priced as Standard.
func Rate(region, class string) float64 {
	rates := storageRates[Region(region)]
	if r, ok := rates[normalizeClass(class)]; ok {
		return r
	}
	return rates[Standard]
}

// ObjectCost returns the monthly storage cost in USD of one object of size
// bytes in a storage class, applying S3's minimum billable size and per-object
// archive overhead. Intelligent-Tiering is priced at its Frequent Access tier,
// since a listing does not say which tier an object is in; that is an upper
// bound for monitored objects and exact for objects under 128 KB.
func ObjectCost(region, class string, size int64) float64 {
	region = Region(region)
	class = normalizeClass(class)
	rate := Rate(region, class)
	switch class {
	case StandardIA, OneZoneIA, GlacierIR:
		if size < minBillable {
			size = minBillable
		}
	case Glacier, DeepArchive:
		overhead := float64(archiveOverhead)*rate + float64(standardOverhead)*Rate(region, Standard)
		return (float64(size)*rate + overhead) / gb
	}
	return float64(size) * rate / gb
}

func normalizeClass(class string) string {
	class = strings.ToUpper(strings.TrimSpace(class))
	if class == "" {
		return Standard
	}
	return class
}
