//go:build orc

// Package orc implements a schema reader for Apache ORC files.
//
// ORC stores its schema in a Footer protobuf message located just before the
// trailing PostScript. We read the PostScript (always uncompressed), then the
// Footer (uncompressed or ZLIB-compressed), and decode the Type tree to recover
// column names and types plus the exact row count. Other footer codecs
// (Snappy/LZO/LZ4/Zstd) are recognized but the type tree is left empty; the
// file is still catalogued as ORC. Parsing never fails the crawl.
package orc

import (
	"bytes"
	"compress/flate"
	"context"
	"fmt"
	"io"

	"github.com/apexion/apexion/internal/format"
	"github.com/apexion/apexion/internal/model"
)

// Reader reads ORC schema.
type Reader struct{}

// New returns an ORC reader.
func New() *Reader { return &Reader{} }

func (r *Reader) Format() model.Format { return model.FormatORC }

// orcType kinds (OrcProto.Type.Kind).
const (
	kindBoolean   = 0
	kindByte      = 1
	kindShort     = 2
	kindInt       = 3
	kindLong      = 4
	kindFloat     = 5
	kindDouble    = 6
	kindString    = 7
	kindBinary    = 8
	kindTimestamp = 9
	kindList      = 10
	kindMap       = 11
	kindStruct    = 12
	kindUnion     = 13
	kindDecimal   = 14
	kindDate      = 15
	kindVarchar   = 16
	kindChar      = 17
	kindTsInstant = 18
)

type orcTypeEntry struct {
	kind       int
	subtypes   []uint32
	fieldNames []string
}

func (r *Reader) ReadSchema(ctx context.Context, src format.Source, opts format.Options) (result *format.Result, err error) {
	// Never let a malformed ORC file crash the crawl.
	defer func() {
		if rec := recover(); rec != nil {
			result = &format.Result{Format: model.FormatORC}
			err = nil
		}
	}()

	ra, size, err := src.ReaderAt(ctx)
	if err != nil {
		return nil, fmt.Errorf("reader-at: %w", err)
	}
	if size < 4 {
		return &format.Result{Format: model.FormatORC}, nil
	}

	// Read the last 64KB (enough for PostScript + Footer of typical files).
	tailLen := int64(65536)
	if tailLen > size {
		tailLen = size
	}
	tail := make([]byte, tailLen)
	if _, err := ra.ReadAt(tail, size-tailLen); err != nil && err != io.EOF {
		return nil, fmt.Errorf("read tail: %w", err)
	}

	// Last byte is the PostScript length.
	psLen := int(tail[len(tail)-1])
	if psLen <= 0 || psLen+1 > len(tail) {
		return &format.Result{Format: model.FormatORC}, nil
	}
	psBytes := tail[len(tail)-1-psLen : len(tail)-1]

	ps, err := parsePostScript(psBytes)
	if err != nil {
		return &format.Result{Format: model.FormatORC}, nil
	}

	comp := mapORCCompression(ps.compression)
	res := &format.Result{Format: model.FormatORC, Compression: comp}

	// Footer sits immediately before the PostScript.
	footerEnd := len(tail) - 1 - psLen
	footerStart := footerEnd - int(ps.footerLength)
	if footerStart < 0 || int(ps.footerLength) == 0 {
		return res, nil
	}
	footerRaw := tail[footerStart:footerEnd]

	footerBytes, ok := decompressORC(footerRaw, ps.compression)
	if !ok {
		return res, nil // codec we don't decode; file still catalogued
	}

	types, numRows := parseFooter(footerBytes)
	res.RowCountEstimate = numRows
	res.Fields = fieldsFromTypes(types)
	return res, nil
}

type postScript struct {
	footerLength uint64
	compression  int
}

func parsePostScript(b []byte) (postScript, error) {
	var ps postScript
	ps.compression = 0 // NONE default
	r := newPB(b)
	for !r.eof() {
		field, wire, err := r.readTag()
		if err != nil {
			return ps, err
		}
		switch {
		case field == 1 && wire == wireVarint: // footerLength
			v, err := r.readVarint()
			if err != nil {
				return ps, err
			}
			ps.footerLength = v
		case field == 2 && wire == wireVarint: // compression
			v, err := r.readVarint()
			if err != nil {
				return ps, err
			}
			ps.compression = int(v)
		default:
			if err := r.skip(wire); err != nil {
				return ps, err
			}
		}
	}
	return ps, nil
}

// parseFooter extracts the Type list (field 4) and numberOfRows (field 6).
func parseFooter(b []byte) ([]orcTypeEntry, int64) {
	var types []orcTypeEntry
	var numRows int64
	r := newPB(b)
	for !r.eof() {
		field, wire, err := r.readTag()
		if err != nil {
			break
		}
		switch {
		case field == 4 && wire == wireLen: // repeated Type types
			body, err := r.readBytes()
			if err != nil {
				return types, numRows
			}
			types = append(types, parseType(body))
		case field == 6 && wire == wireVarint: // numberOfRows
			v, _ := r.readVarint()
			numRows = int64(v)
		default:
			if err := r.skip(wire); err != nil {
				return types, numRows
			}
		}
	}
	return types, numRows
}

func parseType(b []byte) orcTypeEntry {
	var t orcTypeEntry
	r := newPB(b)
	for !r.eof() {
		field, wire, err := r.readTag()
		if err != nil {
			break
		}
		switch {
		case field == 1 && wire == wireVarint: // kind
			v, _ := r.readVarint()
			t.kind = int(v)
		case field == 2 && wire == wireVarint: // subtypes (non-packed)
			v, _ := r.readVarint()
			t.subtypes = append(t.subtypes, uint32(v))
		case field == 2 && wire == wireLen: // subtypes (packed)
			body, _ := r.readBytes()
			t.subtypes = append(t.subtypes, readPackedUint32(body)...)
		case field == 3 && wire == wireLen: // fieldNames
			body, _ := r.readBytes()
			t.fieldNames = append(t.fieldNames, string(body))
		default:
			_ = r.skip(wire)
		}
	}
	return t
}

func fieldsFromTypes(types []orcTypeEntry) []format.Field {
	if len(types) == 0 {
		return nil
	}
	root := types[0]
	if root.kind != kindStruct || len(root.fieldNames) == 0 {
		return nil
	}
	fields := make([]format.Field, 0, len(root.fieldNames))
	for i, name := range root.fieldNames {
		var child orcTypeEntry
		if i < len(root.subtypes) && int(root.subtypes[i]) < len(types) {
			child = types[root.subtypes[i]]
		}
		fields = append(fields, format.Field{
			Name:         name,
			Type:         mapORCKind(child.kind),
			PhysicalType: orcKindName(child.kind),
			Nullable:     true,
		})
	}
	return fields
}

func mapORCKind(kind int) model.DataType {
	switch kind {
	case kindBoolean:
		return model.TypeBoolean
	case kindByte, kindShort, kindInt, kindLong:
		return model.TypeInteger
	case kindFloat, kindDouble:
		return model.TypeFloat
	case kindDecimal:
		return model.TypeDecimal
	case kindString, kindVarchar, kindChar:
		return model.TypeString
	case kindBinary:
		return model.TypeBinary
	case kindTimestamp, kindTsInstant:
		return model.TypeTimestamp
	case kindDate:
		return model.TypeDate
	case kindList:
		return model.TypeArray
	case kindMap:
		return model.TypeMap
	case kindStruct, kindUnion:
		return model.TypeStruct
	default:
		return model.TypeString
	}
}

func orcKindName(kind int) string {
	names := map[int]string{
		kindBoolean: "boolean", kindByte: "tinyint", kindShort: "smallint",
		kindInt: "int", kindLong: "bigint", kindFloat: "float", kindDouble: "double",
		kindString: "string", kindBinary: "binary", kindTimestamp: "timestamp",
		kindList: "array", kindMap: "map", kindStruct: "struct", kindUnion: "union",
		kindDecimal: "decimal", kindDate: "date", kindVarchar: "varchar",
		kindChar: "char", kindTsInstant: "timestamp_instant",
	}
	if n, ok := names[kind]; ok {
		return n
	}
	return "unknown"
}

func mapORCCompression(kind int) model.Compression {
	switch kind {
	case 0:
		return model.CompressionNone
	case 1:
		return model.CompressionGzip // ZLIB
	case 2:
		return model.CompressionSnappy
	case 4:
		return model.CompressionLZ4
	case 5:
		return model.CompressionZstd
	default:
		return model.CompressionNone
	}
}

// decompressORC decodes an ORC compressed stream (sequence of chunks). It
// supports NONE and ZLIB (raw DEFLATE). For other codecs it returns ok=false.
func decompressORC(data []byte, compression int) ([]byte, bool) {
	if compression == 0 {
		return data, true
	}
	if compression != 1 { // only ZLIB decoding implemented
		return nil, false
	}
	var out bytes.Buffer
	pos := 0
	for pos+3 <= len(data) {
		header := le3(data[pos : pos+3])
		pos += 3
		isOriginal := header&1 == 1
		chunkLen := int(header >> 1)
		if pos+chunkLen > len(data) {
			break
		}
		chunk := data[pos : pos+chunkLen]
		pos += chunkLen
		if isOriginal {
			out.Write(chunk)
			continue
		}
		fr := flate.NewReader(bytes.NewReader(chunk))
		decoded, err := io.ReadAll(fr)
		fr.Close()
		if err != nil {
			return nil, false
		}
		out.Write(decoded)
	}
	return out.Bytes(), true
}
