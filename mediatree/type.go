// Package mediatree implements the array-based fcontainer Content tree
// (docs/docs/archive/05-data-format.md) and its farc-specific domain roles
// (docs/docs/archive/07-media-tree.md).
package mediatree

import "fmt"

// NodeType is the closed, format-wide (not domain-specific) physical
// representation of a node's Value — docs/docs/archive/05-data-format.md
// §3.1. The docs describe this set by name and size but do not assign it
// numeric codes (unlike Role, which has explicit codes, §3.1 of
// 07-media-tree.md); the codes below are a farc implementation decision,
// fixed in this table order and, per the same append-only discipline as
// Role, never to be renumbered once written to disk.
type NodeType uint8

const (
	TypeVoid NodeType = iota
	TypeUint8
	TypeUint32
	TypeUint64
	TypeInt32
	TypeInt64
	TypeFloat32
	TypeFloat64
	TypeTimestamp
	TypeDuration
	TypeString
	TypeBytes
)

// FixedSize returns the value's size in bytes for fixed-width types, and ok
// is false for the variable-width types (TypeString, TypeBytes) whose size
// is carried in the node's own Size field.
func (t NodeType) FixedSize() (size int, ok bool) {
	switch t {
	case TypeVoid:
		return 0, true
	case TypeUint8:
		return 1, true
	case TypeUint32, TypeInt32, TypeFloat32:
		return 4, true
	case TypeUint64, TypeInt64, TypeFloat64, TypeTimestamp, TypeDuration:
		return 8, true
	case TypeString, TypeBytes:
		return 0, false
	default:
		return 0, false
	}
}

// Variable reports whether t is a variable-width type (string/bytes).
func (t NodeType) Variable() bool {
	_, ok := t.FixedSize()
	return !ok
}

func (t NodeType) String() string {
	switch t {
	case TypeVoid:
		return "void"
	case TypeUint8:
		return "uint8"
	case TypeUint32:
		return "uint32"
	case TypeUint64:
		return "uint64"
	case TypeInt32:
		return "int32"
	case TypeInt64:
		return "int64"
	case TypeFloat32:
		return "float32"
	case TypeFloat64:
		return "float64"
	case TypeTimestamp:
		return "timestamp"
	case TypeDuration:
		return "duration"
	case TypeString:
		return "string"
	case TypeBytes:
		return "bytes"
	default:
		return fmt.Sprintf("NodeType(%d)", uint8(t))
	}
}

// Valid reports whether t is one of the closed set of known types.
func (t NodeType) Valid() bool {
	return t <= TypeBytes
}
