package dal

type Rollup struct {
	Field       string      `json:"field,omitempty"`
	Aggregation string      `json:"aggregation,omitempty"`
	Value       interface{} `json:"value"`
}

type Group struct {
	Values  map[string]interface{} `json:"values"`
	Rollups []Rollup               `json:"rollups,omitempty"`
	Records *RecordSet             `json:"records,omitempty"`
}

type Groups []*Group

func NewGroup() *Group {
	return &Group{
		Values:  make(map[string]interface{}),
		Rollups: make([]Rollup, 0),
	}
}
