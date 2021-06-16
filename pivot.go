package pivot

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ghetzel/go-stockutil/fileutil"
	"github.com/ghetzel/go-stockutil/log"
	"github.com/ghetzel/go-stockutil/sliceutil"
	"github.com/ghetzel/go-stockutil/typeutil"
	"github.com/ghodss/yaml"
	"github.com/ozanturksever/pivot/v4/backends"
	"github.com/ozanturksever/pivot/v4/dal"
	"github.com/ozanturksever/pivot/v4/filter"
)

// create handy type aliases to avoid importing from all over the place
type Backend = backends.Backend
type Model = backends.Mapper
type Collection = dal.Collection
type Record = dal.Record
type RecordSet = dal.RecordSet
type Filter = filter.Filter
type ConnectOptions = backends.ConnectOptions

var MonitorCheckInterval = time.Duration(10) * time.Second
var NetrcFile = ``

// Create a new database connection with the given options.
func NewDatabaseWithOptions(connection string, options ConnectOptions) (DB, error) {
	if cs, err := dal.ParseConnectionString(connection); err == nil {
		if NetrcFile != `` {
			if err := cs.LoadCredentialsFromNetrc(NetrcFile); err != nil {
				return nil, err
			}
		}

		if backend, err := backends.MakeBackend(cs); err == nil {
			// set indexer
			if options.Indexer != `` {
				if ics, err := dal.ParseConnectionString(options.Indexer); err == nil {
					if NetrcFile != `` {
						if err := ics.LoadCredentialsFromNetrc(NetrcFile); err != nil {
							return nil, err
						}
					}

					if err := backend.SetIndexer(ics); err != nil {
						return nil, err
					}
				} else {
					return nil, err
				}
			}

			// TODO: add MultiIndexer if AdditionalIndexers is present

			if !options.SkipInitialize {
				if err := backend.Initialize(); err != nil {
					return nil, err
				}
			}

			return newdb(backend), nil
		} else {
			return nil, err
		}
	} else {
		return nil, err
	}
}

// Create a new database connection with the default options.
func NewDatabase(connection string) (DB, error) {
	return NewDatabaseWithOptions(connection, ConnectOptions{})
}

// Loads and registers a JSON-encoded array of dal.Collection objects into the given DB backend instance.
func LoadSchemataFromFile(filename string) ([]*dal.Collection, error) {
	if file, err := os.Open(filename); err == nil {
		var collections []*dal.Collection
		var merr error

		switch ext := path.Ext(filename); ext {
		case `.json`:
			if err := json.NewDecoder(file).Decode(&collections); err != nil {
				return nil, fmt.Errorf("decode error: %v", err)
			}

		case `.yml`, `.yaml`:
			if data, err := ioutil.ReadAll(file); err == nil {
				if err := yaml.Unmarshal(data, &collections); err != nil {
					return nil, fmt.Errorf("decode error: %v", err)
				}
			} else {
				return nil, err
			}

		default:
			return nil, nil
		}

		for _, collection := range collections {
			merr = log.AppendError(merr, collection.Check())
		}

		return collections, merr
	} else {
		return nil, err
	}
}

// Calls LoadSchemataFromFile from all *.json files in the given directory.
func LoadSchemata(fileOrDirPaths ...string) ([]*dal.Collection, error) {
	var loaded []*dal.Collection
	var filenames []string

	for _, fileOrDirPath := range fileOrDirPaths {
		if fileutil.DirExists(fileOrDirPath) {
			if fns, err := filepath.Glob(filepath.Join(fileOrDirPath, `*.json`)); err == nil {
				filenames = append(filenames, fns...)
			} else {
				return nil, fmt.Errorf("Cannot list directory %q: %v", fileOrDirPath, err)
			}
		} else if fileutil.IsNonemptyFile(fileOrDirPath) {
			filenames = append(filenames, fileOrDirPath)
		} else if fns, err := filepath.Glob(fileOrDirPath); err == nil {
			filenames = append(filenames, fns...)
		} else {
			return nil, fmt.Errorf("Cannot load schemata from %q", fileOrDirPath)
		}
	}

	sort.Strings(filenames)

	for _, filename := range filenames {
		if collections, err := LoadSchemataFromFile(filename); err == nil {
			if len(collections) == 0 {
				continue
			}

			log.Infof("Loaded %d definitions from %v", len(collections), filename)
			loaded = append(loaded, collections...)
		} else {
			return nil, fmt.Errorf("Cannot load schema file %q: %v", filename, err)
		}
	}

	return loaded, nil
}

// Creates all non-existent schemata in the given directory.
func ApplySchemata(fileOrDirPath string, db Backend) error {
	if collections, err := LoadSchemata(fileOrDirPath); err == nil {
		for _, schema := range collections {
			db.RegisterCollection(schema)

			if _, err := db.GetCollection(schema.Name); err == nil {
				continue
			} else if dal.IsCollectionNotFoundErr(err) {
				if err := db.CreateCollection(schema); err == nil {
					log.Noticef("[%v] Created collection %q", db, schema.Name)
				} else {
					log.Errorf("Cannot create collection %q: %v", schema.Name, err)
				}
			} else {
				return fmt.Errorf("Cannot verify collection %q: %v", schema.Name, err)
			}
		}

		return nil
	} else {
		return err
	}
}

// Loads a JSON-encoded array of dal.Record objects from a file into the given DB backend instance.
func LoadFixturesFromFile(filename string, db Backend) error {
	filename = fileutil.MustExpandUser(filename)

	if file, err := os.Open(filename); err == nil {
		var commentRemover = fileutil.NewReadManipulator(file, fileutil.RemoveLinesWithPrefix(`//`, true))

		defer commentRemover.Close()

		var records []*dal.Record
		var allOptional bool = true

		if err := json.NewDecoder(commentRemover).Decode(&records); err == nil {
			var collections []string

			for _, record := range records {
				// if no collection name was explicitly provided, infer it from the filename
				if record.CollectionName == `` {
					record.CollectionName = strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
				}

				if !record.Optional {
					allOptional = false
				}

				collections = append(collections, record.CollectionName)
			}

			collections = sliceutil.UniqueStrings(collections)

			for _, name := range collections {
				if collection, err := db.GetCollection(name); err == nil {
					var i int

					for _, record := range records {
						var err error
						var op int

						if record.CollectionName != name {
							continue
						}

						if typeutil.IsArray(record.ID) {
							if err := record.SetKeys(collection, dal.PersistOperation, sliceutil.Sliceify(record.ID)...); err != nil {
								return fmt.Errorf("%s id %v: %v", name, record.ID, err)
							}
						}

						// work out what we're supposed to do right now
						switch strings.ToLower(record.Operation) {
						case ``:
							if db.Exists(collection.Name, record) {
								op = 1
							} else {
								op = 0
							}
						case `create`:
							op = 0
						case `update`:
							op = 1
						case `delete`:
							op = 2
						default:
							return fmt.Errorf("invalid operation %q; leave blank, or specify %q, %q, or %q", record.Operation, `create`, `update`, `delete`)
						}

						// do the do
						switch op {
						case 0:
							err = db.Insert(collection.Name, dal.NewRecordSet(record))
						case 1:
							err = db.Update(collection.Name, dal.NewRecordSet(record))
						case 2:
							err = db.Delete(collection.Name, record.Keys(collection))
						}

						if err != nil {
							if record.Optional {
								continue
							} else {
								return fmt.Errorf("Cannot load collection %q, record %v: %v", name, record.ID, err)
							}
						}

						i += 1
					}

					log.Infof("Collection %q: loaded %d records", name, i)
				} else if allOptional {
					continue
				} else {
					return fmt.Errorf("Cannot load collection %q: %v", name, err)
				}
			}

			return nil
		} else {
			return fmt.Errorf("Cannot decode fixture file %q: %v", filename, err)
		}
	} else {
		return fmt.Errorf("Cannot load fixture file %q: %v", filename, err)
	}
}

// Calls LoadFixturesFromFile from all *.json files in the given directory.
func LoadFixtures(fileOrDirPath string, db Backend) error {
	if fileutil.DirExists(fileOrDirPath) || strings.Contains(fileOrDirPath, `*`) {
		var glob string

		// if it looks like we were given a wildcard, trust it.  otherwise,
		// add one ourselves.
		if strings.Contains(fileOrDirPath, `*`) {
			glob = fileOrDirPath
		} else {
			glob = filepath.Join(fileOrDirPath, `*.json`)
		}

		if filenames, err := filepath.Glob(glob); err == nil {
			sort.Strings(filenames)

			for _, filename := range filenames {
				if err := LoadFixturesFromFile(filename, db); err != nil {
					return err
				}
			}
		} else {
			return fmt.Errorf("Cannot list directory %q: %v", fileOrDirPath, err)
		}
	} else if fileutil.IsNonemptyFile(fileOrDirPath) {
		return LoadFixturesFromFile(fileOrDirPath, db)
	} else {
		return fmt.Errorf("Cannot load fixtures from %q", fileOrDirPath)
	}

	return nil
}

// A panicky version of backends.Backend.GetCollection
func MustGetCollection(db Backend, name string) *dal.Collection {
	if collection, err := db.GetCollection(name); err == nil {
		return collection
	} else {
		panic(fmt.Sprintf("Cannot get collection %q: %v", name, err))
	}
}
