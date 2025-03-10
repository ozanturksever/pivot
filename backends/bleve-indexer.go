package backends

import (
	"encoding/json"
	"fmt"
	"math"
	"path"
	"strings"
	"time"

	"github.com/blevesearch/bleve"
	"github.com/blevesearch/bleve/analysis/analyzer/custom"
	"github.com/blevesearch/bleve/analysis/char/regexp"
	"github.com/blevesearch/bleve/analysis/token/lowercase"
	"github.com/blevesearch/bleve/analysis/tokenizer/single"
	"github.com/blevesearch/bleve/mapping"
	"github.com/blevesearch/bleve/search/query"
	"github.com/ghetzel/go-stockutil/log"
	"github.com/ghetzel/go-stockutil/sliceutil"
	"github.com/ghetzel/go-stockutil/stringutil"
	"github.com/orcaman/concurrent-map"
	"github.com/ozanturksever/pivot/v4/dal"
	"github.com/ozanturksever/pivot/v4/filter"
)

var BleveBatchFlushCount = 1
var BleveBatchFlushInterval = 10 * time.Second
var BleveIdentityField = `_id`

type bleveDeferredBatch struct {
	batch     *bleve.Batch
	lastFlush time.Time
}

type BleveIndexer struct {
	Indexer
	conn               *dal.ConnectionString
	parent             Backend
	indexCache         map[string]bleve.Index
	indexDeferredBatch cmap.ConcurrentMap
}

func NewBleveIndexer(connection dal.ConnectionString) *BleveIndexer {
	return &BleveIndexer{
		conn:               &connection,
		indexCache:         make(map[string]bleve.Index),
		indexDeferredBatch: cmap.New(),
	}
}

func (self *BleveIndexer) IndexConnectionString() *dal.ConnectionString {
	return self.conn
}

func (self *BleveIndexer) IndexInitialize(parent Backend) error {
	self.parent = parent

	return nil
}

func (self *BleveIndexer) GetBackend() Backend {
	return self.parent
}

func (self *BleveIndexer) IndexRetrieve(collection *dal.Collection, id interface{}) (*dal.Record, error) {
	defer stats.NewTiming().Send(`pivot.indexers.bleve.retrieve_time`)

	if index, err := self.getIndexForCollection(collection); err == nil {

		request := bleve.NewSearchRequest(bleve.NewDocIDQuery([]string{fmt.Sprintf("%v", id)}))

		if results, err := index.Search(request); err == nil {
			if results.Total == 1 {
				return dal.NewRecord(results.Hits[0].ID).SetFields(results.Hits[0].Fields), nil
			} else {
				return nil, fmt.Errorf("Too many results; expected: 1, got: %d", results.Total)
			}
		} else {
			return nil, err
		}
	} else {
		return nil, err
	}
}

func (self *BleveIndexer) IndexExists(collection *dal.Collection, id interface{}) bool {
	if _, err := self.IndexRetrieve(collection, id); err == nil {
		return true
	}

	return false
}

func (self *BleveIndexer) Index(collection *dal.Collection, records *dal.RecordSet) error {
	defer stats.NewTiming().Send(`pivot.indexers.bleve.index_time`)

	if index, err := self.getIndexForCollection(collection); err == nil {
		name := collection.GetIndexName()

		var batch *bleve.Batch

		d, ok := self.indexDeferredBatch.Get(name)

		if ok {
			batch = d.(*bleveDeferredBatch).batch
		} else {
			batch = index.NewBatch()
			self.indexDeferredBatch.Set(name, &bleveDeferredBatch{
				batch:     batch,
				lastFlush: time.Now(),
			})
		}

		for _, record := range records.Records {
			querylog.Debugf("[%T] Adding %v to batch", self, record)

			if err := batch.Index(fmt.Sprintf("%v", record.ID), record.Fields); err != nil {
				return err
			}
		}

		self.checkAndFlushBatches(false)
		return nil
	} else {
		return err
	}
}

func (self *BleveIndexer) checkAndFlushBatches(forceFlush bool) {
	for item := range self.indexDeferredBatch.Iter() {
		name := item.Key
		deferred := item.Val.(*bleveDeferredBatch)

		if deferred.batch != nil {
			shouldFlush := false

			if deferred.batch.Size() >= BleveBatchFlushCount {
				shouldFlush = true
			}

			if time.Since(deferred.lastFlush) >= BleveBatchFlushInterval {
				shouldFlush = true
			}

			if forceFlush {
				shouldFlush = true
			}

			if shouldFlush {
				defer stats.NewTiming().Send(`pivot.indexers.bleve.deferred_batch_flush`)

				if index, err := self.getIndexForCollection(dal.NewCollection(name)); err == nil {

					querylog.Debugf("[%T] Indexing %d records to %s", self, deferred.batch.Size(), name)

					if err := index.Batch(deferred.batch); err == nil {
						deferred.batch = index.NewBatch()
						deferred.lastFlush = time.Now()

						defer func() {
							for _, key := range self.indexDeferredBatch.Keys() {
								self.indexDeferredBatch.Remove(key)
							}
						}()
					} else {
						log.Errorf("[%T] error indexing %d records to %s: %v", self, deferred.batch.Size(), name, err)
					}
				}
			}
		}
	}
}

func (self *BleveIndexer) QueryFunc(collection *dal.Collection, f *filter.Filter, resultFn IndexResultFunc) error {
	defer stats.NewTiming().Send(`pivot.indexers.bleve.query_time`)

	if f.IdentityField == `` {
		f.IdentityField = BleveIdentityField
	}

	if index, err := self.getIndexForCollection(collection); err == nil {
		if bq, err := self.filterToBleveQuery(index, f); err == nil {
			limit := f.Limit

			if limit == 0 || limit > IndexerPageSize {
				limit = IndexerPageSize
			}

			offset := f.Offset
			page := 1
			processed := 0

			// perform requests until we have enough results or the index is out of them
			for {
				request := bleve.NewSearchRequestOptions(bq, limit, offset, false)

				// apply sorting (if specified)
				if f.Sort != nil && len(f.Sort) > 0 {
					request.SortBy(f.Sort)
				}

				// apply restriction on returned fields
				if f.Fields != nil {
					request.Fields = f.Fields
				}

				// perform search
				if results, err := index.Search(request); err == nil {
					querylog.Debugf("[%T] %+v", self, results)

					if len(results.Hits) == 0 {
						return nil
					}

					// totalPages = ceil(result count / page size)
					totalPages := int(math.Ceil(float64(results.Total) / float64(f.Limit)))

					if totalPages <= 0 {
						totalPages = 1
					}

					// call the resultFn for each hit on this page
					for _, hit := range results.Hits {
						if err := resultFn(dal.NewRecord(hit.ID).SetFields(hit.Fields), nil, IndexPage{
							Page:         page,
							TotalPages:   totalPages,
							Limit:        f.Limit,
							Offset:       offset,
							TotalResults: int64(results.Total),
						}); err != nil {
							return err
						}

						processed += 1

						// if we have a limit set and we're at or beyond it
						if f.Limit > 0 && processed >= f.Limit {
							querylog.Debugf("[%T] %d at or beyond limit %d, returning results", self, processed, f.Limit)
							return nil
						}
					}

					// increment offset by the page size we just processed
					page += 1
					offset += len(results.Hits)

					// if the offset is now beyond the total results count
					if uint64(processed) >= results.Total {
						querylog.Debugf("[%T] %d at or beyond total %d, returning results", self, processed, results.Total)
						return nil
					}
				} else {
					return err
				}
			}
		} else {
			return err
		}
	} else {
		return err
	}
}

func (self *BleveIndexer) Query(collection *dal.Collection, f *filter.Filter, resultFns ...IndexResultFunc) (*dal.RecordSet, error) {
	if f.IdentityField == `` {
		f.IdentityField = BleveIdentityField
	}

	return DefaultQueryImplementation(self, collection, f, resultFns...)
}

func (self *BleveIndexer) IndexRemove(collection *dal.Collection, ids []interface{}) error {
	if index, err := self.getIndexForCollection(collection); err == nil {

		batch := index.NewBatch()

		for _, id := range ids {
			batch.Delete(fmt.Sprintf("%v", id))
		}

		return index.Batch(batch)
	} else {
		return err
	}
}

func (self *BleveIndexer) ListValues(collection *dal.Collection, fields []string, f *filter.Filter) (map[string][]interface{}, error) {
	if index, err := self.getIndexForCollection(collection); err == nil {

		if bq, err := self.filterToBleveQuery(index, f); err == nil {
			request := bleve.NewSearchRequestOptions(bq, 0, 0, false)
			request.Fields = []string{}
			idQuery := false

			for _, field := range fields {
				switch field {
				case `_id`, `id`:
					idQuery = true
					request.Size = MaxFacetCardinality
					request.Fields = append(request.Fields, BleveIdentityField)
				default:
					request.AddFacet(
						field,
						bleve.NewFacetRequest(field, MaxFacetCardinality),
					)
				}
			}

			if results, err := index.Search(request); err == nil {
				querylog.Debugf("[%T] %+v", self, results)

				output := make(map[string][]interface{})

				for name, facet := range results.Facets {
					values := make([]interface{}, 0)

					for _, term := range facet.Terms {
						values = append(values, term.Term)
					}

					querylog.Debugf("[%T] facet %q (%d values)", self, name, len(values))
					output[name] = sliceutil.Compact(values)
				}

				if idQuery {
					values := make([]interface{}, 0)

					for _, hit := range results.Hits {
						values = append(values, stringutil.Autotype(hit.ID))
					}

					querylog.Debugf("[%T] facet _id (%d values)", self, len(values))
					output[`id`] = values
				}

				return output, nil
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

func (self *BleveIndexer) DeleteQuery(collection *dal.Collection, f *filter.Filter) error {
	f.Fields = []string{BleveIdentityField}
	var ids []interface{}

	if err := self.QueryFunc(collection, f, func(indexRecord *dal.Record, err error, page IndexPage) error {
		ids = append(ids, indexRecord.ID)
		return nil
	}); err == nil {
		return self.parent.Delete(collection.Name, ids)
	} else {
		return err
	}
}

func (self *BleveIndexer) FlushIndex() error {
	self.checkAndFlushBatches(true)
	return nil
}

func (self *BleveIndexer) getIndexForCollection(collection *dal.Collection) (bleve.Index, error) {
	defer stats.NewTiming().Send(`pivot.indexers.bleve.retrieve_index`)
	name := collection.GetIndexName()

	if v, ok := self.indexCache[name]; ok {
		return v, nil
	} else {
		var index bleve.Index
		mapping := bleve.NewIndexMapping()

		// setup the mapping and text analysis settings for this index
		self.useFilterMapping(mapping)

		switch self.conn.Dataset() {
		case `memory`:
			if ix, err := bleve.NewMemOnly(mapping); err == nil {
				index = ix
			} else {
				return nil, err
			}
		default:
			indexBaseDir := self.conn.Dataset()
			indexPath := path.Join(indexBaseDir, name)

			if ix, err := bleve.Open(indexPath); err == nil {
				index = ix
			} else if ix, err := bleve.New(indexPath, mapping); err == nil {
				index = ix
			} else {
				return nil, err
			}
		}

		self.indexCache[name] = index
		return index, nil
	}
}

func (self *BleveIndexer) filterToBleveQuery(index bleve.Index, f *filter.Filter) (query.Query, error) {
	defer stats.NewTiming().Send(`pivot.indexers.bleve.filter_to_native`)

	if f.MatchAll {
		return bleve.NewMatchAllQuery(), nil
	} else {
		mapping := index.Mapping()
		conjunction := bleve.NewConjunctionQuery()

		for _, criterion := range f.Criteria {
			// map any field called "id" to the identity field name
			if criterion.Field == `id` {
				if f.IdentityField == `` {
					criterion.Field = BleveIdentityField
				} else {
					criterion.Field = f.IdentityField
				}
			}

			var skipNext bool
			var disjunction *query.DisjunctionQuery

			analyzerName := mapping.AnalyzerNameForPath(criterion.Field)

			// this handles AND (field=a OR b OR ...)
			if len(criterion.Values) > 1 {
				disjunction = bleve.NewDisjunctionQuery()
			}

			for _, vI := range criterion.Values {
				value := fmt.Sprintf("%v", vI)
				var analyzedValue string
				var invertQuery bool

				if az := mapping.AnalyzerNamed(analyzerName); az != nil {
					for _, token := range az.Analyze([]byte(value[:])) {
						analyzedValue += string(token.Term[:])
					}
				} else {
					analyzedValue = value
				}

				var currentQuery query.FieldableQuery

				switch criterion.Operator {
				case `is`, ``, `not`, `like`, `unlike`:
					switch criterion.Operator {
					case `not`, `unlike`:
						invertQuery = true
					}

					if criterion.Field == f.IdentityField {
						q := bleve.NewDocIDQuery(sliceutil.Stringify(criterion.Values))

						if invertQuery {
							bq := bleve.NewBooleanQuery()
							bq.AddMustNot(q)
							conjunction.AddQuery(bq)
						} else {
							conjunction.AddQuery(q)
						}

						skipNext = true
						break
					} else {
						switch analyzedValue {
						case `null`:
							currentQuery = bleve.NewTermQuery(``)
						case `true`:
							currentQuery = bleve.NewBoolFieldQuery(true)
						case `false`:
							currentQuery = bleve.NewBoolFieldQuery(false)
						default:
							currentQuery = bleve.NewTermQuery(analyzedValue)
						}
					}

				case `prefix`:
					currentQuery = bleve.NewWildcardQuery(analyzedValue + `*`)
				case `suffix`:
					currentQuery = bleve.NewWildcardQuery(`*` + analyzedValue)
				case `contains`:
					currentQuery = bleve.NewWildcardQuery(`*` + analyzedValue + `*`)

				case `gt`, `lt`, `gte`, `lte`:
					var minInc, maxInc bool

					if strings.HasPrefix(criterion.Operator, `gt`) {
						minInc = strings.HasSuffix(criterion.Operator, `e`)
					} else {
						maxInc = strings.HasSuffix(criterion.Operator, `e`)
					}

					switch criterion.Type {
					case dal.TimeType:
						var min, max time.Time

						if v, err := stringutil.ConvertToTime(analyzedValue); err == nil {
							if strings.HasPrefix(criterion.Operator, `gt`) {
								min = v
							} else {
								max = v
							}
						} else {
							return nil, err
						}

						currentQuery = query.NewDateRangeInclusiveQuery(min, max, &minInc, &maxInc)
					default:
						var min, max *float64

						if v, err := stringutil.ConvertToFloat(analyzedValue); err == nil {
							if strings.HasPrefix(criterion.Operator, `gt`) {
								min = &v
							} else {
								max = &v
							}
						} else {
							return nil, err
						}

						currentQuery = bleve.NewNumericRangeInclusiveQuery(min, max, &minInc, &maxInc)
					}

				// case `not`:
				// 	q := bleve.NewBooleanQuery()
				// 	var subquery query.FieldableQuery

				// 	if analyzedValue == `null` {
				// 		subquery = bleve.NewTermQuery(``)
				// 	} else {
				// 		subquery = bleve.NewTermQuery(analyzedValue)
				// 	}

				// 	subquery.SetField(criterion.Field)
				// 	q.AddMustNot(subquery)

				// 	if disjunction != nil {
				// 		disjunction.AddQuery(q)
				// 		conjunction.AddQuery(disjunction)
				// 	}else{
				// 		conjunction.AddQuery(q)
				// 	}

				// 	continue

				default:
					return nil, fmt.Errorf("Unimplemented operator '%s'", criterion.Operator)
				}

				if currentQuery != nil {
					currentQuery.SetField(criterion.Field)

					if invertQuery {
						inversionQuery := bleve.NewBooleanQuery()
						inversionQuery.AddMustNot(currentQuery)

						if disjunction != nil {
							disjunction.AddQuery(inversionQuery)
						} else {
							conjunction.AddQuery(inversionQuery)
						}
					} else {
						if disjunction != nil {
							disjunction.AddQuery(currentQuery)
						} else {
							conjunction.AddQuery(currentQuery)
						}
					}
				}
			}

			if skipNext {
				continue
			}

			if disjunction != nil {
				conjunction.AddQuery(disjunction)
			}
		}

		if len(conjunction.Conjuncts) > 0 {
			data, _ := json.MarshalIndent(conjunction, ``, `  `)
			querylog.Debugf("[%T] Query: %v", self, string(data[:]))

			return conjunction, nil
		} else {
			return nil, fmt.Errorf("Filter did not produce a valid query")
		}
	}
}

func (self *BleveIndexer) useFilterMapping(mappingImpl *mapping.IndexMappingImpl) {
	mappingImpl.AddCustomCharFilter(`remove_expression_tokens`, map[string]interface{}{
		`type`:   regexp.Name,
		`regexp`: `[\:\[\]\*]+`,
	})

	mappingImpl.AddCustomAnalyzer(`pivot_filter`, map[string]interface{}{
		`type`: custom.Name,
		`char_filters`: []string{
			`remove_expression_tokens`,
		},
		`tokenizer`: single.Name,
		`token_filters`: []string{
			lowercase.Name,
		},
	})

	mappingImpl.DefaultAnalyzer = `pivot_filter`
}
