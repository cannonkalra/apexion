//go:build orc

package orc

import (
	"encoding/binary"
	"errors"
)

// A tiny protobuf wire-format reader sufficient to parse the ORC PostScript and
// Footer messages. Only the field types ORC uses are handled.

const (
	wireVarint  = 0
	wireFixed64 = 1
	wireLen     = 2
	wireFixed32 = 5
)

type pbReader struct {
	buf []byte
	pos int
}

func newPB(b []byte) *pbReader { return &pbReader{buf: b} }

func (r *pbReader) eof() bool { return r.pos >= len(r.buf) }

func (r *pbReader) readVarint() (uint64, error) {
	var x uint64
	var s uint
	for {
		if r.pos >= len(r.buf) {
			return 0, errors.New("varint: eof")
		}
		b := r.buf[r.pos]
		r.pos++
		if b < 0x80 {
			if s >= 64 {
				return 0, errors.New("varint: overflow")
			}
			x |= uint64(b) << s
			return x, nil
		}
		x |= uint64(b&0x7f) << s
		s += 7
	}
}

// readTag returns the field number and wire type of the next field.
func (r *pbReader) readTag() (field int, wire int, err error) {
	key, err := r.readVarint()
	if err != nil {
		return 0, 0, err
	}
	return int(key >> 3), int(key & 7), nil
}

func (r *pbReader) readBytes() ([]byte, error) {
	n, err := r.readVarint()
	if err != nil {
		return nil, err
	}
	if r.pos+int(n) > len(r.buf) {
		return nil, errors.New("bytes: overrun")
	}
	b := r.buf[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return b, nil
}

// skip advances past a field of the given wire type.
func (r *pbReader) skip(wire int) error {
	switch wire {
	case wireVarint:
		_, err := r.readVarint()
		return err
	case wireFixed64:
		if r.pos+8 > len(r.buf) {
			return errors.New("fixed64: overrun")
		}
		r.pos += 8
		return nil
	case wireLen:
		_, err := r.readBytes()
		return err
	case wireFixed32:
		if r.pos+4 > len(r.buf) {
			return errors.New("fixed32: overrun")
		}
		r.pos += 4
		return nil
	default:
		return errors.New("unknown wire type")
	}
}

// readPackedUint32 decodes a packed repeated uint32 field body.
func readPackedUint32(b []byte) []uint32 {
	r := newPB(b)
	var out []uint32
	for !r.eof() {
		v, err := r.readVarint()
		if err != nil {
			break
		}
		out = append(out, uint32(v))
	}
	return out
}

// le3 reads a 3-byte little-endian unsigned int (ORC compression chunk header).
func le3(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16
}

var _ = binary.LittleEndian
