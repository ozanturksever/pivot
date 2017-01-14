package mapper

import (
	"github.com/ghetzel/pivot"
	"github.com/ghetzel/pivot/dal"
	"github.com/stretchr/testify/require"
	"io/ioutil"
	"os"
	"testing"
)

func TestModelCRUD(t *testing.T) {
	assert := require.New(t)

	tmpfile, err := ioutil.TempFile("", "byteflood-TestModelCRUD")
	assert.Nil(err)
	defer os.Remove(tmpfile.Name())

	db, err := pivot.NewDatabase(`sqlite://` + tmpfile.Name())
	assert.Nil(err)

	type ModelOne struct {
		ID      int
		Name    string `pivot:"name"`
		Enabled bool   `pivot:"enabled,omitempty"`
		Size    int    `pivot:"size,omitempty"`
	}

	model1 := NewModel(db, dal.Collection{
		Name: `model_one`,
		Fields: []dal.Field{
			{
				Name: `name`,
				Type: dal.StringType,
			}, {
				Name: `enabled`,
				Type: dal.BooleanType,
			}, {
				Name: `size`,
				Type: dal.IntType,
			},
		},
	})

	assert.Nil(model1.Migrate())
	assert.Nil(model1.Create(&ModelOne{
		ID:      1,
		Name:    `test-1`,
		Enabled: true,
		Size:    12345,
	}))

	v := new(ModelOne)
	err = model1.Get(1, v)

	assert.Nil(err)
	assert.Equal(1, v.ID)
	assert.Equal(`test-1`, v.Name)
	assert.Equal(true, v.Enabled)
	assert.Equal(12345, v.Size)

	v.Name = `testerly-one`
	assert.Nil(model1.Update(v))

	v = new(ModelOne)
	err = model1.Get(1, v)

	assert.Nil(err)
	assert.Equal(1, v.ID)
	assert.Equal(`testerly-one`, v.Name)
	assert.Equal(true, v.Enabled)
	assert.Equal(12345, v.Size)

	assert.Nil(model1.Delete(1))
	assert.Error(model1.Get(1, nil))
}

func TestModelFind(t *testing.T) {
	assert := require.New(t)

	tmpfile, err := ioutil.TempFile("", "byteflood-TestModelFind")
	assert.Nil(err)
	defer os.Remove(tmpfile.Name())

	db, err := pivot.NewDatabase(`sqlite://` + tmpfile.Name())
	assert.Nil(err)

	type ModelTwo struct {
		ID      int
		Name    string `pivot:"name"`
		Enabled bool   `pivot:"enabled,omitempty"`
		Size    int    `pivot:"size,omitempty"`
	}

	model := NewModel(db, dal.Collection{
		Name: `model_one`,
		Fields: []dal.Field{
			{
				Name: `name`,
				Type: dal.StringType,
			}, {
				Name: `enabled`,
				Type: dal.BooleanType,
			}, {
				Name: `size`,
				Type: dal.IntType,
			},
		},
	})

	assert.Nil(model.Migrate())
	assert.Nil(model.Create(&ModelTwo{
		ID:      1,
		Name:    `test-one`,
		Enabled: true,
		Size:    12345,
	}))

	assert.Nil(model.Create(&ModelTwo{
		ID:      2,
		Name:    `test-two`,
		Enabled: false,
		Size:    98765,
	}))

	assert.Nil(model.Create(&ModelTwo{
		ID:      3,
		Name:    `test-three`,
		Enabled: true,
	}))

	var resultsStruct []ModelTwo
	assert.Error(model.All(resultsStruct))

	assert.Nil(model.All(&resultsStruct))
	assert.Equal(3, len(resultsStruct))

	var recordset dal.RecordSet

	assert.Error(model.All(recordset))
	assert.Nil(model.All(&recordset))
	assert.Equal(int64(3), recordset.ResultCount)
}
