package format

import (
	"bufio"
	"compress/bzip2"
	"compress/gzip"
	"fmt"
	"io"

	"github.com/klauspost/compress/s2"
	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"

	"github.com/apexion/apexion/internal/model"
)

// Decompress wraps r with the appropriate decompressor for comp. The returned
// reader must be fully consumed by the caller; it is valid for the lifetime of
// r. For unknown/none codecs r is returned as-is.
func Decompress(r io.Reader, comp model.Compression) (io.Reader, error) {
	switch comp {
	case model.CompressionNone, "":
		return r, nil
	case model.CompressionGzip:
		return gzip.NewReader(bufio.NewReader(r))
	case model.CompressionBzip2:
		return bzip2.NewReader(r), nil
	case model.CompressionZstd:
		zr, err := zstd.NewReader(r)
		if err != nil {
			return nil, err
		}
		return zr.IOReadCloser(), nil
	case model.CompressionSnappy:
		// Object-store "snappy" text files use the framed (S2/snappy) format.
		return s2.NewReader(r), nil
	case model.CompressionLZ4:
		return lz4.NewReader(r), nil
	default:
		return nil, fmt.Errorf("unsupported compression: %s", comp)
	}
}
