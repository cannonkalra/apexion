package json

import "github.com/apexion/apexion/internal/features"

// JSON and JSONL are core formats: always compiled, always registered.
func init() {
	features.RegisterReader(NewJSON())
	features.RegisterReader(NewJSONL())
}
