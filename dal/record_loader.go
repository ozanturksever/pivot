package dal

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/fatih/structs"
	"github.com/ghetzel/go-stockutil/sliceutil"
)

var RecordStructTag = `pivot`
var DefaultStructIdentityFieldName = `ID`

type fieldDescription struct {
	Field        *structs.Field
	ReflectField reflect.Value
	Identity     bool
	OmitEmpty    bool
}

type Model interface{}

// Retrieves the struct field name and key name that represents the identity field for a given struct.
func getIdentityFieldNameFromStruct(instance interface{}, fallbackIdentityFieldName string) (string, string, error) {
	if err := validatePtrToStructType(instance); err != nil {
		return ``, ``, err
	}

	s := structs.New(instance)

	// find a field with an ",identity" tag and get its value
	for _, field := range s.Fields() {
		if tag := field.Tag(RecordStructTag); tag != `` {
			v := strings.Split(tag, `,`)

			if sliceutil.ContainsString(v[1:], `identity`) {
				if v[0] != `` {
					return field.Name(), v[0], nil
				} else {
					return field.Name(), field.Name(), nil
				}
			}
		}
	}

	if fallbackIdentityFieldName == `` {
		fallbackIdentityFieldName = DefaultStructIdentityFieldName
	}

	if _, ok := s.FieldOk(fallbackIdentityFieldName); ok {
		return fallbackIdentityFieldName, fallbackIdentityFieldName, nil
	} else if _, ok := s.FieldOk(DefaultStructIdentityFieldName); ok {
		return DefaultStructIdentityFieldName, DefaultStructIdentityFieldName, nil
	}

	return ``, ``, fmt.Errorf("No identity field could be found for type %T", instance)
}

func validatePtrToStructType(instance interface{}) error {
	vInstance := reflect.ValueOf(instance)

	if vInstance.IsValid() {
		if vInstance.Kind() == reflect.Ptr {
			vInstance = vInstance.Elem()
		} else {
			return fmt.Errorf("Can only operate on pointer to struct, got %T", instance)
		}

		if vInstance.Kind() == reflect.Struct {
			return nil
		} else {
			return fmt.Errorf("Can only operate on pointer to struct, got %T", instance)
		}
	} else {
		return fmt.Errorf("invalid value %T", instance)
	}
}

func getFieldsForStruct(instance interface{}) (map[string]fieldDescription, error) {
	fields := make(map[string]fieldDescription)
	identitySet := false

	reflectStruct := reflect.ValueOf(instance)

	if reflectStruct.Kind() == reflect.Ptr {
		reflectStruct = reflectStruct.Elem()
	}

	if reflectStruct.Kind() != reflect.Struct {
		return nil, fmt.Errorf("value must be a struct")
	}

	instanceStruct := structs.New(instance)

	for _, field := range instanceStruct.Fields() {
		var identity, omitEmpty bool

		name := field.Name()

		// read struct tags to determine how values are mapped to struct fields
		if tag := field.Tag(RecordStructTag); tag != `` {
			v := strings.Split(tag, `,`)

			// if the first value isn't an empty string, that's what we're calling the field
			if v[0] != `` {
				name = v[0]
			}

			// set additional flags from tag options
			if len(v) > 1 {
				identity = sliceutil.ContainsString(v[1:], `identity`)
				omitEmpty = sliceutil.ContainsString(v[1:], `omitempty`)
			}
		}

		if !identitySet && identity {
			identitySet = true
		}

		fields[name] = fieldDescription{
			Field:        field,
			ReflectField: reflectStruct.FieldByName(field.Name()),
			Identity:     identity,
			OmitEmpty:    omitEmpty,
		}
	}

	return fields, nil
}
