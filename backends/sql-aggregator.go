package backends

// this file satifies the Aggregator interface for SqlBackend

import (
	"database/sql"
	"reflect"

	"github.com/ghetzel/go-stockutil/typeutil"
	"github.com/ghetzel/pivot/v3/dal"
	"github.com/ghetzel/pivot/v3/filter"
	"github.com/ghetzel/pivot/v3/filter/generators"
)

type sqlAggResultFunc func(*sql.Rows, *generators.Sql, *dal.Collection, *filter.Filter) (interface{}, error)

func (self *SqlBackend) Sum(collection *dal.Collection, field string, f ...*filter.Filter) (float64, error) {
	return self.aggregateFloat(collection, filter.Sum, field, f)
}

func (self *SqlBackend) Count(collection *dal.Collection, f ...*filter.Filter) (uint64, error) {
	whatToCount := collection.IdentityField

	if typeutil.IsZero(whatToCount) {
		whatToCount = `1`
	}

	v, err := self.aggregateFloat(collection, filter.Count, whatToCount, f)
	return uint64(v), err
}

func (self *SqlBackend) Minimum(collection *dal.Collection, field string, f ...*filter.Filter) (float64, error) {
	return self.aggregateFloat(collection, filter.Minimum, field, f)
}

func (self *SqlBackend) Maximum(collection *dal.Collection, field string, f ...*filter.Filter) (float64, error) {
	return self.aggregateFloat(collection, filter.Maximum, field, f)
}

func (self *SqlBackend) Average(collection *dal.Collection, field string, f ...*filter.Filter) (float64, error) {
	return self.aggregateFloat(collection, filter.Average, field, f)
}

func (self *SqlBackend) GroupBy(collection *dal.Collection, groupBy []string, aggregates []filter.Aggregate, f ...*filter.Filter) (dal.Groups, error) {
	groups := make(dal.Groups, 0)

	if _, err := self.aggregate(collection, groupBy, aggregates, f, func(
		rows *sql.Rows,
		_ *generators.Sql,
		_ *dal.Collection,
		_ *filter.Filter,
	) (interface{}, error) {
		if columns, err := rows.Columns(); err == nil {
			for rows.Next() {
				group := dal.NewGroup()
				values := make([]interface{}, len(columns))

				for i := 0; i < len(groupBy); i++ {
					var v interface{}
					values[i] = &v
				}

				for i := 0; i < len(aggregates); i++ {
					switch aggregates[i].Aggregation {
					case filter.Count:
						v := uint64(0)
						values[len(groupBy)+i] = &v
					default:
						v := float64(0)
						values[len(groupBy)+i] = &v
					}
				}

				if err := rows.Scan(values...); err == nil {
					for i, v := range values {
						if v != nil {
							switch v.(type) {
							case *sql.NullString:
								if nv := v.(*sql.NullString); nv.Valid {
									values[i] = nv.String
								} else {
									values[i] = nil
								}
							case *sql.NullBool:
								if nv := v.(*sql.NullBool); nv.Valid {
									values[i] = nv.Bool
								} else {
									values[i] = nil
								}
							case *sql.NullInt64:
								if nv := v.(*sql.NullInt64); nv.Valid {
									values[i] = nv.Int64
								} else {
									values[i] = nil
								}
							case *sql.NullFloat64:
								if nv := v.(*sql.NullFloat64); nv.Valid {
									values[i] = nv.Float64
								} else {
									values[i] = nil
								}
							default:
								values[i] = typeutil.ResolveValue(v)
							}
						}
					}

					for i := 0; i < len(groupBy); i++ {
						if typeutil.IsZero(values[i]) {
							group.Values[typeutil.String(groupBy[i])] = nil
						} else {
							group.Values[typeutil.String(groupBy[i])] = values[i]
						}
					}

					for i := 0; i < len(aggregates); i++ {
						group.Rollups = append(group.Rollups, dal.Rollup{
							Field:       aggregates[i].Field,
							Aggregation: aggregates[i].Aggregation.String(),
							Value:       typeutil.Float(values[len(groupBy)+i]),
						})
					}

					groups = append(groups, group)
				} else {
					return nil, err
				}
			}
		} else {
			return nil, err
		}

		return nil, nil
	}); err == nil {
		return groups, nil
	} else {
		return nil, err
	}
}

func (self *SqlBackend) aggregateFloat(collection *dal.Collection, aggregation filter.Aggregation, field string, f []*filter.Filter) (float64, error) {
	if result, err := self.aggregate(collection, nil, []filter.Aggregate{
		{
			Aggregation: aggregation,
			Field:       field,
		},
	}, f, self.extractSingleFloat64); err == nil {
		return result.(float64), nil
	} else {
		return 0, err
	}
}

func (self *SqlBackend) aggregate(
	collection *dal.Collection,
	groupBy []string,
	aggregates []filter.Aggregate,
	f []*filter.Filter,
	resultFn sqlAggResultFunc,
) (interface{}, error) {
	queryGen := self.makeQueryGen(collection)
	var flt *filter.Filter

	if len(f) == 0 {
		flt = filter.New()
	} else {
		flt = f[0]
	}

	for _, g := range groupBy {
		queryGen.GroupByField(g)
	}

	for _, agg := range aggregates {
		queryGen.AggregateByField(agg.Aggregation, agg.Field)
	}

	if err := queryGen.Initialize(collection.Name); err == nil {
		if stmt, err := filter.Render(queryGen, collection.Name, flt); err == nil {
			querylog.Debugf("[%T] %s %v", self, string(stmt[:]), queryGen.GetValues())

			// perform query
			if rows, err := self.db.Query(string(stmt[:]), queryGen.GetValues()...); err == nil {
				defer rows.Close()
				return resultFn(rows, queryGen, collection, flt)
			} else {
				return nil, err
			}
		} else {
			return nil, err
		}
	} else {
		return nil, err
	}
}

func (self *SqlBackend) AggregatorConnectionString() *dal.ConnectionString {
	return self.GetConnectionString()
}

func (self *SqlBackend) AggregatorInitialize(parent Backend) error {
	return nil
}

func (self *SqlBackend) extractSingleFloat64(rows *sql.Rows, _ *generators.Sql, _ *dal.Collection, _ *filter.Filter) (interface{}, error) {
	if rows.Next() {
		var rv sql.NullFloat64

		if err := rows.Scan(&rv); err == nil {
			return rv.Float64, nil
		} else {
			return float64(0), err
		}
	} else {
		return float64(0), nil
	}
}

func (self *SqlBackend) extractRecordSet(rows *sql.Rows, queryGen *generators.Sql, collection *dal.Collection, flt *filter.Filter) (interface{}, error) {
	recordset := dal.NewRecordSet()

	if columns, err := rows.Columns(); err == nil {
		for rows.Next() {
			if record, err := self.scanFnValueToRecord(queryGen, collection, columns, reflect.ValueOf(rows.Scan), flt.Fields); err == nil {
				recordset.Push(record)
			} else {
				return nil, err
			}
		}
	} else {
		return nil, err
	}

	return recordset, nil
}
