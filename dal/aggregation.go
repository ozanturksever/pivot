package dal

type Rollup struct {
	Field       string      `json:"field,omitempty"`
	Aggregation string      `json:"aggregation,omitempty"`
	Value       interface{} `json:"value"`
}

type Group struct {
	ID      map[string]interface{} `json:"id"`
	Rollups []Rollup               `json:"rollups,omitempty"`
	Records *RecordSet             `json:"records,omitempty"`
}

type Groups []*Group

func NewGroup() *Group {
	return &Group{
		ID:      make(map[string]interface{}),
		Rollups: make([]Rollup, 0),
	}
}
