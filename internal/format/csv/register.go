package csv

import "github.com/apexion/apexion/internal/features"

// CSV and TSV are core formats: always compiled, always registered.
func init() {
	features.RegisterReader(NewCSV())
	features.RegisterReader(NewTSV())
}
