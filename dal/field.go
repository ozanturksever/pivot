package dal

import (
	"github.com/ghetzel/go-stockutil/stringutil"
)

type FieldProperties struct {
	Identity     bool        `json:"identity,omitempty"`
	Key          bool        `json:"key,omitempty"`
	Required     bool        `json:"required,omitempty"`
	Unique       bool        `json:"unique,omitempty"`
	DefaultValue interface{} `json:"default,omitempty"`
	NativeType   string      `json:"native_type,omitempty"`
}

type Field struct {
	Name       string           `json:"name"`
	Type       string           `json:"type"`
	Length     int              `json:"length,omitempty"`
	Precision  int              `json:"precision,omitempty"`
	Properties *FieldProperties `json:"properties,omitempty"`
}

func (self *Field) ConvertValue(in interface{}) (interface{}, error) {
	var convertType stringutil.ConvertType

	switch self.Type {
	case `str`:
		convertType = stringutil.String
	case `bool`:
		convertType = stringutil.Boolean
	case `float`:
		convertType = stringutil.Float
	case `int`:
		convertType = stringutil.Integer
	case `date`, `time`:
		convertType = stringutil.Time
	default:
		return in, nil
	}

	return stringutil.ConvertTo(convertType, in)
}
