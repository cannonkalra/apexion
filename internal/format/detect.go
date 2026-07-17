package format

import (
	"bytes"
	"path"
	"strings"

	"github.com/apexion/apexion/internal/model"
)

// IsHidden reports whether an object key should be ignored (dotfiles, Spark
// temp/success markers, checkpoints).
func IsHidden(key string) bool {
	base := path.Base(key)
	if base == "" {
		return false
	}
	if strings.HasPrefix(base, ".") || strings.HasPrefix(base, "_") {
		// Keep table-format metadata markers; the caller recognizes these
		// directories explicitly. Individual _-prefixed data files are noise.
		switch {
		case base == "_delta_log", base == "_SUCCESS":
			return true
		default:
			return true
		}
	}
	// Common non-data marker files.
	switch base {
	case "_SUCCESS", "_metadata", "_common_metadata":
		return true
	}
	return false
}

// DetectCompression infers a codec from the key's extension.
func DetectCompression(key string) model.Compression {
	lower := strings.ToLower(key)
	switch {
	case strings.HasSuffix(lower, ".gz"), strings.HasSuffix(lower, ".gzip"):
		return model.CompressionGzip
	case strings.HasSuffix(lower, ".zst"), strings.HasSuffix(lower, ".zstd"):
		return model.CompressionZstd
	case strings.HasSuffix(lower, ".snappy"):
		return model.CompressionSnappy
	case strings.HasSuffix(lower, ".bz2"), strings.HasSuffix(lower, ".bzip2"):
		return model.CompressionBzip2
	case strings.HasSuffix(lower, ".lz4"):
		return model.CompressionLZ4
	case strings.HasSuffix(lower, ".br"):
		return model.CompressionBrotli
	default:
		return model.CompressionNone
	}
}

// stripCompressionExt removes a trailing compression extension so the format
// extension underneath can be examined (e.g. data.csv.gz -> data.csv).
func stripCompressionExt(key string) string {
	lower := strings.ToLower(key)
	for _, ext := range []string{".gz", ".gzip", ".zst", ".zstd", ".snappy", ".bz2", ".bzip2", ".lz4", ".br"} {
		if strings.HasSuffix(lower, ext) {
			return key[:len(key)-len(ext)]
		}
	}
	return key
}

// Magic byte prefixes.
var (
	magicParquet = []byte("PAR1")
	magicORC     = []byte("ORC")
	magicAvro    = []byte("Obj\x01")
)

// DetectFormat classifies an object from its key extension and, when provided,
// a small header sample. header may be nil.
func DetectFormat(key string, header []byte) model.Format {
	// Magic bytes are the most reliable signal.
	if len(header) >= 4 {
		if bytes.HasPrefix(header, magicParquet) {
			return model.FormatParquet
		}
		if bytes.HasPrefix(header, magicAvro) {
			return model.FormatAvro
		}
		if bytes.HasPrefix(header, magicORC) {
			return model.FormatORC
		}
	}

	base := strings.ToLower(path.Base(stripCompressionExt(key)))
	ext := path.Ext(base)
	switch ext {
	case ".parquet", ".pq":
		return model.FormatParquet
	case ".orc":
		return model.FormatORC
	case ".avro":
		return model.FormatAvro
	case ".csv":
		return model.FormatCSV
	case ".tsv", ".tab":
		return model.FormatTSV
	case ".jsonl", ".ndjson":
		return model.FormatJSONL
	case ".json":
		// Distinguish array/object JSON from line-delimited by peeking.
		if looksLikeJSONL(header) {
			return model.FormatJSONL
		}
		return model.FormatJSON
	}

	// Fall back to content sniffing for extensionless keys.
	if looksLikeJSONL(header) {
		return model.FormatJSONL
	}
	if h := bytes.TrimSpace(header); len(h) > 0 && (h[0] == '{' || h[0] == '[') {
		return model.FormatJSON
	}
	return model.FormatUnknown
}

// looksLikeJSONL reports whether the header looks like newline-delimited JSON
// objects (each non-empty line starts with '{').
func looksLikeJSONL(header []byte) bool {
	if len(header) == 0 {
		return false
	}
	lines := bytes.SplitN(header, []byte("\n"), 4)
	objectLines := 0
	for _, ln := range lines {
		t := bytes.TrimSpace(ln)
		if len(t) == 0 {
			continue
		}
		if t[0] != '{' {
			return false
		}
		objectLines++
	}
	return objectLines >= 2
}
