package zcl

import "errors"

// ZCL data type IDs.
const (
	// DataTypeCharString is a ZCL character string (length-prefixed UTF-8).
	DataTypeCharString byte = 0x42
)

// Basic cluster attribute IDs.
const (
	AttrManufacturerName uint16 = 0x0004
	AttrModelIdentifier  uint16 = 0x0005
	AttrSWBuildID        uint16 = 0x4000
)

// BasicClusterID is the ZCL Basic cluster (0x0000).
const BasicClusterID uint16 = 0x0000

// AttributeValue holds a decoded ZCL attribute.
type AttributeValue struct {
	Status   uint8 // 0x00 = success, 0x86 = unsupported attribute
	DataType uint8
	Value    any // string for CharString, nil for unsupported/error
}

var errFrameTooShort = errors.New("zcl: frame too short")
